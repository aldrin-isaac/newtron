# Design Principles

This document describes the architectural principles behind newtron,
newtlab, and newtrun — and the philosophy that keeps them coherent as a
system. It is intended to be read before the HLDs and LLDs, as a guide
for understanding *why* things are the way they are.

---

## 1. SONiC Is a Database — Treat It as One

SONiC's architecture is a set of Redis databases — CONFIG_DB (desired
state), APP_DB (computed routes from FRR), ASIC_DB (SAI forwarding
objects), STATE_DB (operational telemetry) — with daemons that react to
table changes. This is not an implementation detail; it is the
architecture.

newtron interacts with SONiC exclusively through Redis. CONFIG_DB writes
go through a native Go Redis client over an SSH-tunneled connection —
not through `config` CLI commands. Route verification reads APP_DB
directly. ASIC programming checks traverse ASIC_DB's SAI object chain.
Health checks read STATE_DB.

This matters because the alternative — SSHing in and parsing CLI
output — is fragile in ways that are invisible until they break. `show
ip route` output varies between SONiC releases. `config vlan add`
returns exit code 0 even when it silently fails. Text parsing adds a
translation layer between what the device knows and what the tool sees.
Redis eliminates that layer: the data structures are the interface.

When Redis cannot express an operation (persisting config to disk,
restarting daemons, reading platform files), CLI commands are used as
documented exceptions. Each is tagged `CLI-WORKAROUND` with a resolution
path — a note on what upstream change would eliminate the workaround.
The goal is to reduce these over time, not normalize them.

---

## 2. Platform Patching — Fix Bugs, Don't Reinvent Features

SONiC runs on many platforms — VPP, CiscoVS (Silicon One), and others —
and not all of them implement every community feature correctly. When a
platform has a bug that prevents a SONiC feature from working, the right
response is to fix the broken behavior so it works as the community
intended. The wrong response is to invent a parallel mechanism that
routes around the broken code.

The test is simple: **does the fix use the same CONFIG_DB signals and
perform the same intended actions?** If yes, it's a valid bug fix. If it
introduces a new table, a new schema, or a new code path that replaces
the community-intended mechanism, it's reinvention.

A concrete example of a valid fix: `frrcfgd`'s `vrf_handler` has a bug
on CiscoVS that prevents VNI programming from propagating into zebra.
The community-intended flow is: write `VRF` table in CONFIG_DB → frrcfgd
reads it → runs `vtysh vrf X; vni N`. The fix is `newtron-vni-poll` — a
polling fallback that reads the **same standard signal** (`VRF` table)
and performs the **same intended action** (`vtysh vrf X; vni N`). Polling
instead of pub/sub is an implementation detail of the fix, not a
reinvention of the feature. Callers still write the same `VRF` table
entries they always did.

A concrete example of reinvention: inventing a custom `NEWTRON_VNI`
CONFIG_DB table, writing VNI assignments there, and having the polling
daemon scan that table instead of `VRF`. Now callers must write to a
non-standard table. Community daemons that scan `VRF` won't see the
assignment. When SONiC upgrades `frrcfgd`, the bug may be fixed — but
the custom table remains, creating permanent maintenance debt.

The same principle applies to SAI-level failures. When
`VNI_TO_VIRTUAL_ROUTER_ID` fails on Silicon One, the correct response is
to document it as a known SAI limitation (an RCA entry) and fix it at
the SAI layer if possible. Routing around it by repurposing shadow VLANs
or different code paths invents a mechanism the community never intended,
which interacts unpredictably with the rest of SONiC.

SONiC is a large community project. Invented mechanisms interact
unpredictably with community daemons, break across SONiC upgrades, and
create maintenance debt that compounds with every platform it's applied
to. **Patch what's broken; don't build parallel infrastructure around it.**

---

## 3. Two Tools and an Orchestrator

The system is split into three programs — newtron (provision devices),
newtlab (deploy VMs), newtrun (E2E testing) — not because three is a
nice number, but because each program has a fundamentally different
relationship with the world:

- **newtlab** realizes a topology. It reads newtron's `topology.json` and
  brings it to life — deploying QEMU VMs (primarily SONiC) and wiring
  them together using socket-based links across one or more servers. No
  root, no bridges, no Docker. It doesn't define the topology or touch
  device configuration — it makes the topology physically exist.
- **newtron** defines opinionated configuration primitives for SONiC —
  one pattern per unit of CONFIG_DB configuration — and delivers them
  safely to devices. The configuration architecture is in the primitives;
  the topology architecture is the operator's choice. newtron operates on
  a single device at a time, translating specs into CONFIG_DB through an
  SSH tunnel. It never talks to two devices at once.
- **newtrun** is an orchestrator specifically for E2E testing. It tests
  two things: that newtron's automation produces correct device state,
  and that SONiC software on each device behaves correctly in its role
  (spine, leaf, etc.). It deploys topologies (via newtlab), provisions
  devices (via newtron), then asserts correctness — both per-device
  and across the fabric.

newtron and newtlab are general-purpose tools. newtrun is not — it exists
to test newtron and the SONiC stack. Other orchestrators could be built on top of
newtron and newtlab for different purposes (production deployment,
CI/CD pipelines, compliance auditing), and the architecture is designed
to support that. newtron's observation primitives (`GetRoute`,
`RunHealthChecks`) return structured data precisely so that *any*
orchestrator can consume them — not just newtrun.

These boundaries follow from a single rule: **each program owns exactly
one level of abstraction**. newtlab owns VM realization — turning a
topology spec into running, connected VMs. newtron owns single-device
configuration — translating specs into CONFIG_DB entries. Orchestrators
own the "what, where, and in what order" — which devices to provision,
which services to apply to which interfaces, with what parameters, and
in what sequence. newtrun is the first orchestrator, focused on E2E
testing.

If you're unsure where something belongs, ask: "does this decide what
gets applied where, or how something gets applied?" The former belongs
in an orchestrator. The latter belongs in newtron. "Does this require
knowing about device configuration at all?" If no, it belongs in newtlab.

---

## 4. Objects Own Their Methods — The Interface Is the Point of Service

newtron uses an object-oriented architecture where methods belong to the
object that has the context to execute them. This is the most important
structural decision in the system, and it is driven by a networking
truth: **the interface is the point of service**.

In networking, everything happens at the interface. Policy attaches to
an interface. VRF binding, VLAN membership, ACL application, QoS
scheduling, BGP peering — all are per-interface. The interface is where
abstract service intent meets physical infrastructure. It is:

- **The point of service delivery** — where specs bind to physical ports
- **The unit of service lifecycle** — apply, remove, refresh happen
  per-interface
- **The unit of state** — each interface has exactly one service binding
  (or none)
- **The unit of isolation** — services on Ethernet0 and Ethernet4 are
  independent

This is not a code-organization choice. It is the fundamental
abstraction of the domain. A network *is*, at its core, services
applied on interfaces.

An `Interface` knows its parent `Node`, which knows its parent
`Network`. When you call `Interface.ApplyService()`, the interface
reaches up to the Node for the AS number, up to the Network (via
SpecProvider) for the service spec, and combines them with its own
identity to produce CONFIG_DB entries. No external function orchestrates
this — the object has everything it needs through its parent chain.

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
  │ (SpecProvider)
  ▼
┌─────────────────────────────────────────────────┐
│                                                 │
│                      Node                       │
│ owns: profile, resolved config, Redis, ConfigDB │
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

Interface delegates to Node for infrastructure (Redis connections,
CONFIG_DB cache, specs) just as a VLAN interface on a real switch
delegates to the forwarding ASIC for packet processing. The delegation
does not make Interface a "forwarding layer" — it makes Interface a
logical point of attachment that the underlying infrastructure services.

Interface-scoped config functions are methods on Interface, not free
functions that take an interface name parameter. The Interface knows its
own name — requiring callers to pass it separately is an abstraction
leak. See Principle 24 (Respect Abstraction Boundaries).

This means:

- **ApplyService lives on Interface**, not on Node or Network, because
  the interface is the entity being configured — the point where a
  service becomes real. The interface's identity (name, IP, parent
  node) is part of the translation context.

- **VerifyChangeSet lives on Node**, not on Network or in a utility
  package, because the node holds the Redis connection needed to re-read
  CONFIG_DB.

- **GetService lives on Network**, not on Node, because services are
  network-wide definitions that exist independent of any device.

The CLI mirrors this: `newtron -N network -D device -i interface verb`.
You select an object, then invoke a method on it. The flags are not
configuration — they are object selection.

The general principle: **a method belongs to the smallest object that has
all the context to execute it**. If an operation needs the interface name,
the device profile, and the network specs, it lives on Interface (which
can reach all three through its parent chain). If it only needs the Redis
connection, it lives on Node.

Node convenience methods (e.g., `Node.ApplyQoS(intfName, ...)`) resolve a
name string to an Interface and delegate — they never re-implement the
operation. The real logic always lives on Interface.

This principle extends to configuration itself: **whatever can be
right-shifted to the interface level, should be**. BGP is the clearest
example. eBGP neighbors are interface-specific — they derive from the
interface's IP and the service's peer AS, so they belong to interface
configuration via `ApplyService`. Route reflector peering (iBGP toward
spines) is device-specific — it derives from the device's role and site
topology, so it belongs to device-level setup via `SetupRouteReflector`.
The rule is the same as for methods: push configuration down to the
most specific entity that fully determines it. Interface-level config
is more composable, more independently testable, and easier to
reason about than device-level config that happens to mention an
interface.

---

## 5. If You Change It, You Verify It

This is the verification principle, and it governs the boundary between
newtron and any orchestrator that uses it:

> If a tool changes the state of an entity, that tool must be able to
> verify the change had the intended effect. The caller should not need
> a second tool to find out if the first tool worked.

newtron writes CONFIG_DB. So newtron must be able to confirm its writes
landed. This is `VerifyChangeSet` — it re-reads CONFIG_DB through a
fresh connection and diffs against the ChangeSet that was just applied.

newtron configures BGP redistribution. So newtron must be able to read
APP_DB to see if the route appeared locally. This is `GetRoute` — it
returns the route entry (or nil) from the device's own routing table.

But newtron does not — and cannot — verify that a route *propagated to
another device*. That requires connecting to the other device and
checking its routing table. That's topology-wide context. That belongs
in the orchestrator — whatever orchestrator that may be.

The corollary is equally important: **orchestrators do not re-implement
newtron's verification**. When newtrun needs to check CONFIG_DB on a
device, it calls newtron's `VerifyChangeSet`. When it needs to read a
route, it calls newtron's `GetRoute`. Orchestrators *compose* newtron's
primitives across devices — they never duplicate them. A future
production orchestrator would do the same.

This creates a clean four-tier verification hierarchy:

| Tier | Question | Who Answers |
|------|----------|-------------|
| CONFIG_DB | "Did my writes land?" | newtron (assertion) |
| APP_DB | "Did the route appear locally?" | newtron (observation) |
| Operational | "Is the device healthy?" | newtron (observation) |
| Cross-device | "Did the route reach the neighbor?" | orchestrator (assertion) |

Notice the distinction between *assertion* and *observation*. newtron
asserts only one thing: that its own writes are in CONFIG_DB. Everything
else it returns as structured data — a `RouteEntry`, a health report —
and lets the caller decide what's correct. This is because newtron,
operating on a single device, cannot know what "correct" means for
routing state. Correctness requires topology context that only the
orchestrator has.

newtron provides the *mechanism* to check things — consistent with the
changes it makes. Deciding *when* to check, *what* to check, and *what
to do* if a check fails is the orchestrator's job.

---

## 6. Prevent Bad Writes, Don't Just Detect Them

Verification happens after a write. But CONFIG_DB has no referential
integrity — it accepts entries that reference nonexistent tables,
contradictory bindings, and overlapping resources without complaint.
SONiC daemons respond to invalid config with silent failures, crashes,
or undefined behavior.

newtron prevents these writes rather than detecting them after the fact.
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

For removal operations, `DependencyChecker` scans CONFIG_DB to determine
if shared resources (VRFs, VLANs, ACLs) are still referenced by other
service bindings before deleting them. A VRF used by three interfaces
isn't removed until the last interface unbinds from it.

The principle: **it is better to refuse an invalid write than to detect
the damage afterward**. Preconditions make the invalid states
unrepresentable at the API level.

### Error Classification — "Doesn't Exist" vs "Can't Be Touched"

Pre-operation checks fall into two categories, and they must return
different error types:

- **"Resource doesn't exist"** — the VLAN, VRF, interface, or ACL table
  the operation targets is not present in CONFIG_DB. These checks go
  through `PreconditionChecker` and return `PreconditionError` (wrapping
  `ErrPreconditionFailed`). Examples: `RequireVLANExists`,
  `RequireVRFExists`, `GetInterface` when the interface is not in
  CONFIG_DB.

- **"Resource exists but can't be safely modified"** — the resource is
  present but has active consumers or dependencies that prevent the
  operation. These are domain safety checks that return plain errors.
  Examples: `DeleteVRF` refusing because interfaces are still bound,
  `UnbindIPVPN` refusing because service bindings still reference the
  VRF.

The distinction matters for automated recovery. When rolling back a
partially-applied operation, "doesn't exist" means the forward operation
never created the resource — safe to skip. "Has active consumers" means
something unexpected happened — the recovery tool should stop and let
the operator investigate. Both are pre-operation checks, but they carry
different semantic weight and demand different responses from the caller.

Every "resource not found" check — whether in the `PreconditionChecker`,
a lookup method like `GetInterface`, or an inline existence check — must
return `PreconditionError`. This is not a style preference; it is a
correctness requirement for any code path that needs to distinguish
"missing" from "conflicting."

---

## 7. Spec vs Config — Intent vs State

The system enforces an absolute separation between what the operator
*wants* (spec) and what the device *has* (config). These are different
things with different lifecycles, different storage, and different
owners.

**Specs** are declarative design constraints. They say "this interface
should have service customer-l3 with BGP peering" — defining what the
network *must* look like while leaving room for the specifics of each
deployment (different IPs, AS numbers, topologies). They use names and
references, never concrete values. They live in JSON files, are
version-controlled, and are authored by network architects.

**Config** is imperative. It says "VRF|Vrf-customer-Ethernet0 exists
with vni=3001 and vrf_reg_mask=0." It uses concrete values — IPs,
VLAN IDs, AS numbers. It lives in Redis on each device, is generated
at runtime, and is never hand-edited.

The translation from spec to config happens inside newtron's object
hierarchy, using device context (profile, platform, site) to derive
concrete values. A service spec says `"peer_as": "request"` — newtron
resolves this to a concrete AS number from the device profile or
topology parameters. A filter reference says `"ingress_filter":
"customer-in"` — newtron expands this into numbered ACL rules from the
filter definition.

This separation enables two things that matter:

1. **The same spec applied to different devices produces different
   config** — because the concrete values come from each device's
   context, not from the spec itself.

2. **The same spec applied twice to the same device produces identical
   config** — because the translation is deterministic. This is what
   makes reprovisioning idempotent. newtron enforces this actively —
   it checks preconditions before acting (e.g., whether a service
   already exists on an interface) rather than blindly re-applying.

The spec directory is also the only coupling surface between the three
programs. newtlab reads topology and platform specs to deploy VMs. newtron
reads all specs to provision devices. newtrun reads scenario definitions
that reference topology specs. No program imports another's packages or
calls another's API. They communicate through files.

Config is generated by newtron and not intended to be hand-edited. But
if external changes are made to CONFIG_DB — by an admin, another tool,
or a SONiC daemon — newtron treats the device state as authoritative.
Incremental operations read and respect what's on the device, not what
the spec says should be there. See Principle 8.

---

## 8. The Device Is Source of Reality

The device CONFIG_DB is ground reality — what exists on the device,
whether correct or not. Spec files are templates and intent, but once
configuration is applied, the device state is what matters. If an admin
edits CONFIG_DB directly — via the SONiC CLI, Redis, or another tool —
that edit is the new reality. newtron does not fight it or attempt to
reconcile back to the spec.

This shapes every operation in the system, but different operation types
interact with device reality differently:

- **Provisioning (CompositeOverwrite)** is the one exception where intent
  replaces reality — it merges a full composite config on top of CONFIG_DB,
  removing stale keys while preserving factory defaults (MAC, platform
  metadata, port config).
- **Basic operations** (CreateVLAN, ConfigureBGP) read CONFIG_DB to check
  preconditions before acting — "does this VLAN already exist?" — but
  generate entries from specs and profile, not from device state.
- **Service operations** trust the binding record as ground reality.
  `ApplyService` reads CONFIG_DB for idempotency filtering on shared
  infrastructure (does the VLAN or VRF already exist?). `RemoveService`
  reads the NEWTRON_SERVICE_BINDING record — not CONFIG_DB tables, not
  specs — to determine what to tear down.

**NEWTRON_SERVICE_BINDING records live on the device**, not in spec
files. When a service is applied to an interface, newtron writes a
binding record to CONFIG_DB that captures exactly what was applied —
which VLANs, VRFs, ACLs, and VNIs were created for that service. The
binding is the ground reality of what was applied, and the sole input
for teardown. `RemoveService` does not re-derive the removal from
the spec because the spec may have changed, and what matters is what was
actually applied.

**Bindings must be self-sufficient for reverse operations.** The binding
record must contain every value the reverse path needs to tear down what
the forward path created. `RemoveService` must never re-resolve specs at
removal time — the spec may have changed between apply and remove, and
the binding records what was *actually applied*. For example, `l3vni`
and `l3vni_vlan` are stored in the binding so `RemoveService` can tear
down transit VLAN infrastructure without looking up the IP-VPN spec.
When adding a new forward operation that creates infrastructure, the
question to ask is: *can the reverse operation find everything it needs
in the binding alone?* If not, the binding is incomplete.

**Idempotency filtering** also operates on device reality, not spec
intent. When applying a service, newtron checks whether VLANs and VRFs
already exist on the device — because they may have been created by a
different service or an external tool. It respects what's already there
rather than blindly re-creating it. This check happens against
CONFIG_DB, not against a spec-derived expected state.

This is why newtron is not a Terraform or Kubernetes desired-state
reconciler — and why it does not support brownfield. A reconciler needs a
single canonical source of desired state to diff the device against. For
incremental operations, no such canonical source exists — the "desired
state" of the device is its current state plus the requested change, and
the current state can only be read from the device itself. And two
opinionated architectures cannot converge on the same device — newtron's
device-reality checks minimize harm, they do not accommodate existing
config from a different architectural model.

**The device is the reality; specs are the intent; operations transform
reality using intent.**

---

## 9. Hierarchical Spec Resolution — Network, Zone, Node

Specs are organized in a three-level hierarchy: network → zone → node
(device profile). Each level can define the same seven overridable spec
maps: Services, Filters, IPVPNs, MACVPNs, QoSPolicies,
RoutePolicies, and PrefixLists. **Lower level wins.** A service defined
at the node level overrides the same-named service at the zone level,
which overrides the network level.

This means a network architect can define standard service templates at
the network level — available to every device. A zone (e.g.,
"datacenter-east") can specialize those templates or add zone-specific
services. An individual device can override further, for example to
use a different filter set on a particular edge node. You never need to
copy-paste a full spec at every level; you only define what differs.

**Platforms are global-only.** Platform definitions (CiscoVS, VPP, etc.)
don't participate in the zone or node hierarchy because they describe
hardware capabilities — HWSKU, port count, NIC driver — not network
intent. Platform capabilities are globally shared and have no meaningful
per-zone or per-node variation.

The merge is performed once by `buildResolvedSpecs()` in `network.go`,
which produces a `ResolvedSpecs` struct for each node. `ResolvedSpecs`
implements the `node.SpecProvider` interface. The Node receives its
resolved specs at creation time and never queries the hierarchy again —
it sees a flat, merged view where all overrides have already been
applied.

This design cleanly separates two concerns: **what specs exist** (the
three-level hierarchy, owned by Network) and **what specs does this node
see** (the merged view, owned by Node via its SpecProvider). Code inside
`node/` does not know about zones, networks, or override logic. It asks
its SpecProvider for a service by name and gets back the right definition
— already resolved.

**Define once at the broadest applicable scope; override only where
necessary; resolve once at node creation.**

---

## 10. Don't Repeat Yourself — Across Programs

DRY applies not just within a single codebase, but across the entire
system. Every capability exists in exactly one place:

**One spec directory.** newtlab, newtron, and newtrun all read from the
same `specs/` directory. `topology.json` belongs to newtlab and newtrun —
it defines the physical topology for VM deployment and test orchestration.
newtron does not require it. newtlab reads the `devices` and `links`
sections to deploy VMs. newtrun reads the topology to understand device
layout for test scenarios. Neither maintains its own copy.

**One connection mechanism.** SSH-tunneled Redis access is implemented in
newtron's device layer. When an orchestrator needs to read a device's
CONFIG_DB, it uses newtron's connection — not its own SSH implementation.
When it needs to check a route, it calls newtron's `GetRoute` — not its
own Redis query.

**One verification mechanism.** The ChangeSet is the universal
verification contract. Every mutating operation — disaggregated
(`CreateVLAN`) or composite (`DeliverComposite`) — produces a ChangeSet.
`VerifyChangeSet` works on any ChangeSet. There are no per-operation
verify methods, no table-specific assertion helpers, no parallel
verification paths. One mechanism for everything.

**One platform definition.** `platforms.json` describes each platform
once. newtlab reads VM-specific fields (image, memory, NIC driver).
newtron reads hardware fields (HWSKU, port count). Orchestrators read
capability fields (dataplane support). Each consumer takes what it needs
from the same definition.

**One profile per device.** A device profile starts with operator-authored
fields (loopback IP, site, platform). newtlab adds runtime fields (SSH port,
console port, management IP). newtron reads the combined profile. There
is no separate newtlab state file that newtron must also consult — newtlab
writes its output *into the same profile* newtron already reads.

The anti-pattern this prevents: an orchestrator implementing its own
CONFIG_DB reader "because it needs a slightly different format." Or
newtlab maintaining its own device inventory "because it needs extra
fields." Every time a capability is duplicated, the copies drift. The
system prevents drift by having one authoritative implementation of
each capability.

---

## 11. Single-Owner CONFIG_DB Tables

Each CONFIG_DB table has exactly one owner — a single file and function
responsible for constructing, writing, and deleting entries in that
table. Composite operations (ApplyService, SetupEVPN, ConfigureLoopback,
topology provisioning) call the owning primitives and merge their
ChangeSets rather than constructing entries inline.

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

Why does this matter? CONFIG_DB entry construction is more subtle than
it looks. Field names, key formats, and value encodings are
platform-specific and daemon-specific — what `vrfmgrd` expects is not
what `vxlanmgrd` expects. If two files independently construct
`VLAN|Vlan100` entries with slightly different field logic, the system
breaks silently when one path is updated and the other isn't. There is
no compiler check for this — the inconsistency shows up at runtime, in
a daemon log, hours later.

Single ownership eliminates this class of bug. When the `VLAN` table
format changes, you change `vlan_ops.go`. Every caller — `service_gen.go`,
`topology.go`, the CLI — calls into `vlan_ops.go` and gets the updated
format automatically. The change propagates through callers, not beside
them.

This applies at every layer. If `vlan_ops.go` owns `VLAN` table writes,
then `service_gen.go` must call into `vlan_ops.go`, not duplicate the
entry construction. Convenience is not a justification for a second
writer. **One file owns each table; everyone else calls the owner.**

---

## 12. File-Level Feature Cohesion

Code should be organized so that a reader can guess where a feature is
implemented by looking at file names alone. This is stronger than the
Single-Owner principle — it's not just about who writes a CONFIG_DB
table, it's about where all the code for a feature lives.

All code for a feature — types, reads, writes, existence checks, list
operations — belongs in one file. `GetVLAN` and `VLANInfo` belong in
`vlan_ops.go` just as much as `CreateVLAN` does. A reader who wants to
understand how VLANs work in newtron should be able to open one file
and find everything: how entries are constructed, how existing VLANs are
detected, how memberships are queried. If "reads" live in one file and
"writes" live in another, the reader has to reconstruct the feature from
two sources.

Four file roles define the boundaries:

- **`composite.go`** = delivery mechanics only. `CompositeBuilder`,
  `CompositeConfig`, `DeliverComposite`. No CONFIG_DB table or key
  format knowledge. It knows how to deliver a ChangeSet; it does not
  know what the entries mean.
- **`topology.go`** = topology-driven provisioning orchestration. It
  calls config functions from `node/` and assembles their results, but
  it never constructs CONFIG_DB keys inline. "What gets provisioned
  where" — not "how entries are built."
- **Each `*_ops.go`** = sole owner of its CONFIG_DB tables' entry
  construction. One file per feature; one feature per file.
- **`service_gen.go`** = service-to-entries translation. Calls config
  functions from the owning `*_ops.go` files and merges their output.
  It doesn't construct entries directly.

The practical test: "does this new line of code construct a CONFIG_DB
key or table entry?" If yes, it belongs in the owning `*_ops.go` file.
If it calls a config function and adds the result to a ChangeSet, it
belongs in the caller. This test is mechanical enough to apply
consistently.

**If you want to understand a feature, read one file. If you want to
change a table format, change one file.**

---

## 13. Import Direction — Dependencies Flow One Way

The `network/` and `network/node/` packages have a strict one-way import
direction: `network/` imports `network/node/`, never the reverse. This
is not merely a Go compiler requirement — it is a design boundary that
prevents Node operations from reaching into Network state.

Network creates Nodes and passes them specs. If Node could import
Network, the packages would form a circular dependency. More importantly,
it would mean that code inside a Node operation could call Network
methods, read Network state, or mutate Network data structures. Node
operations would no longer be self-contained; they'd be entangled with
the orchestration layer above them.

The **`SpecProvider` interface** breaks what would otherwise be a hard
circular dependency. Network implements `SpecProvider` via
`ResolvedSpecs`. A Node accepts a `SpecProvider` at creation time — an
interface, not a concrete type. The Node can access its resolved specs
without importing or knowing anything about Network. The dependency flows
through an abstraction: Network → SpecProvider interface → Node.

The same pattern applies at every level of the system. Device-level
code (`pkg/newtron/device/sonic/`) does not import network-level code.
CLI code (`cmd/newtron/`) imports packages but packages never import CLI
code. Spec types are shared across packages via a dedicated types
package; package-internal types are not exported.

Why this matters in practice: when you change a Node method, you know
the blast radius is `node/` plus its callers in `network/`. You don't
need to worry about Network internals being affected by the change.
When you change a Network method, Node code is provably untouched — the
import direction guarantees it. This property degrades gracefully: as
the system grows, the blast radius of any change stays bounded by the
import graph.

**Dependencies flow from orchestration to primitives, never backward.
Interfaces break the cycles.**

---

## 14. Dry-Run as a First-Class Mode

Every mutating operation in newtron supports dry-run mode — not as an
afterthought, but as the **default behavior**. The `-x` flag is required
to execute. Without it, operations preview what would change and return.

This is not just a safety feature. It's an architectural constraint that
shapes how operations are written. Because every operation must be able to
*compute* its changes without *applying* them, the translation logic is
necessarily separate from the execution logic. You can't write an
operation that "figures out what to do as it goes" — the ChangeSet must
be fully computed before any Redis writes happen.

This separation has a useful side effect: composite mode. Because
newtron can compute a full device configuration without connecting to
the device (it's just spec translation), it can build a CompositeConfig
offline and deliver it later as a single atomic operation. The same
dry-run / execute split that makes the CLI safe also makes offline
provisioning possible.

---

## 15. Programs Communicate Through Files, Not APIs

newtlab does not expose an API that newtron calls. newtron does not expose
a service that newtrun connects to. The programs are not microservices.

Instead, they communicate through the spec directory:

- newtlab writes `ssh_port`, `console_port`, and `mgmt_ip` into profile
  files after deploying VMs.
- newtron reads those profile files and uses the ports to connect.
- Orchestrators (like newtrun) invoke newtlab and newtron as CLI commands,
  passing the spec directory path.

This means:

- **No shared libraries.** A change to newtron's internal types does not
  require rebuilding newtlab.
- **No runtime coordination.** newtlab exits after deploying. newtron exits
  after provisioning. They don't need to be alive at the same time.
- **No service discovery.** newtron doesn't ask "where is newtlab's API?"
  It reads a file.
- **Portability.** The spec directory is the complete, self-contained
  state of the system. Copy it to another machine, run the tools, get
  the same result.

The spec directory is the system's API. The programs are its
implementations.

---

## 16. Observation vs Assertion

newtron's verification primitives fall into two categories, and the
distinction matters:

**Assertions** check newtron's own work. `VerifyChangeSet` asserts that
the CONFIG_DB entries newtron just wrote are actually present. If this
fails, it's a bug in newtron. There is exactly one assertion primitive.

**Observations** return device state as structured data. `GetRoute`
returns a route entry (or nil). `GetRouteASIC` returns a resolved SAI
chain. `RunHealthChecks` returns a health report. These methods don't
know what the "correct" answer is — they just report what they see.

This distinction exists because correctness depends on scope. newtron
*knows* what it wrote to CONFIG_DB, so it can assert. newtron *doesn't
know* what routes should be present (that depends on what other devices
are advertising), so it can only observe.

The rule: **return data, not judgments**. A method that returns a
`RouteEntry` is useful to any caller. A method that returns `true`/`false`
for "is this route correct?" encodes assumptions about what "correct"
means — assumptions that break when the calling context changes.

---

## 17. The ChangeSet Is the Universal Contract

Every mutating operation produces a ChangeSet — an ordered list of
CONFIG_DB mutations with table, key, operation type, old value, and new
value. The ChangeSet serves three roles:

1. **Dry-run preview** — display what would change
2. **Execution receipt** — record of what was written
3. **Verification contract** — `VerifyChangeSet` diffs against live state

Because every operation produces a ChangeSet, there is exactly one
verification method that works for everything. Creating a VLAN produces
a ChangeSet with one entry. Applying a service produces a ChangeSet with
a dozen entries. Delivering a composite config produces a ChangeSet with
hundreds of entries. `VerifyChangeSet` handles all of them identically.

This eliminates an entire class of bugs: "we added a new operation but
forgot to add its verification method." If it produces a ChangeSet, it's
automatically verifiable.

A ChangeSet is atomic within a single newtron invocation — whether that
invocation configures one interface or delivers a full composite config.
If an orchestrator makes multiple newtron invocations (e.g., provisioning
three interfaces in sequence) and the second fails, deciding whether to
roll back the first is the orchestrator's responsibility. newtron provides
the mechanism (each invocation's ChangeSet can be reversed); the
orchestrator decides the policy.

### Replace Semantics Require DEL+HSET

Redis `HSET` merges fields into an existing hash — it does not remove
old fields. Any operation that replaces a key's content
(`RefreshService`, re-provisioning) must `DEL` the key first, then
`HSET` the new fields. Without the `DEL`, stale fields from the
previous state persist as ghost data. For example, if a service binding
previously had `qos_policy=gold` and the new service definition drops
QoS, an `HSET` leaves the old `qos_policy` field intact — only
`DEL`+`HSET` gives a clean replacement.

This has two consequences for ChangeSets:

- **Apply must preserve delete+add sequences.** When a merged ChangeSet
  contains both a delete and a subsequent add for the same key (e.g.,
  `RefreshService` = remove + apply), both operations must be sent to
  Redis in order. SONiC daemons see the delete notification, tear down
  their internal state for that key, then see the add notification and
  build new state from clean inputs. Stripping the intermediate delete
  would leave stale fields and prevent daemons from cleaning up.

- **Verification checks final state only.** When verifying a merged
  ChangeSet, `verifyConfigChanges` computes the last operation per key.
  A key that was deleted then re-added is verified as "should exist with
  new fields" — not as "should be deleted." The apply sequence handles
  intermediate state; the verifier only cares about the end result.

---

## 18. Episodic Caching — Fresh Snapshot per Unit of Work

newtron caches CONFIG_DB in memory to batch precondition checks into a
single `GetAll()` call instead of a Redis round-trip per check. But a
cache is only useful if you know when it's fresh and when it's stale.
The rule is simple:

> Every self-contained unit of work — an **episode** — begins with a
> fresh CONFIG_DB snapshot. No episode relies on cache from a prior
> episode.

An episode is any code path that reads the cache for a purpose:

- **Write episodes** (`ExecuteOp`): `Lock()` refreshes the cache after
  acquiring the distributed lock. Precondition checks within the
  operation read from this snapshot. `Apply()` writes to Redis without
  reloading — the episode is ending.

- **Read-only episodes** (`RunHealthChecks`, CLI show commands):
  `Refresh()` at the start loads a current snapshot.

- **Composite episodes** (provisioning): `Refresh()` after delivery
  reloads the cache to reflect the bulk write.

The key design choice is *where* the refresh happens. It does not happen
after `Apply()` — that would reload the cache at the end of an episode,
serving no one. It happens at the *start* of the next episode, where it
serves the code that's about to read. This means between episodes the
cache may be stale, and that's fine — no code reads it there.

This is not transactional isolation. A SONiC device is a shared resource
— admins, other tools, and SONiC daemons can write to CONFIG_DB at any
time. The distributed lock coordinates newtron instances but cannot
prevent external writes. The precondition checks are **advisory safety
nets**: they catch common mistakes (duplicate VRF, non-existent VLAN)
but cannot prevent all race conditions. This is acceptable — the
alternative (Redis WATCH/MULTI transactions for reads) would add
fundamental complexity for marginal benefit in environments where
newtron is typically the sole CONFIG_DB writer.

The principle: **cache freshness is a property of episodes, not of
individual reads**. Refresh once at the start, read many times within,
never carry state across episode boundaries.

---

## 19. CLI-First Research — Verify Before You Implement

Before writing any CONFIG_DB entries to implement a SONiC feature,
follow a five-step protocol. Never assume a CONFIG_DB path works without
first verifying it via CLI on a real device.

1. **CLI-first research.** Find the SONiC CLI command that enables the
   feature. Read the `sonic-utilities` or `sonic-mgmt-framework` source
   to see exactly what CONFIG_DB tables and fields those commands write,
   in what order, and what pre/post steps they take.

2. **Empirical verification.** On a freshly deployed (clean) SONiC node,
   configure the feature using only SONiC CLI commands — not newtron.
   Verify it works end-to-end. Capture the resulting CONFIG_DB state
   (`redis-cli -n 4 KEYS '*'`) as ground truth.

3. **Framework audit.** Read the relevant SONiC daemon source (`vrfmgrd`,
   `vxlanmgrd`, `orchagent`, `frrcfgd`) to understand how each CONFIG_DB
   entry is processed, what APP_DB and ASIC_DB entries it creates, and
   what ordering constraints exist.

4. **Implement in newtron.** Make newtron write the same CONFIG_DB entries
   in the same order as the CLI path. Do not invent alternative layouts
   without explicit authorization.

5. **Targeted test first.** Create a targeted newtrun suite that tests
   only the specific feature. Debug and pass it before integrating into
   composite suites.

Why this matters: SONiC CONFIG_DB entries are processed by daemons with
implicit ordering dependencies, undocumented field interactions, and
platform-specific SAI behaviors. The only reliable way to know what
works is to observe the CLI path on a real device first. Reading the
SONiC schema does not tell you what `orchagent` expects; reading the
daemon source does not tell you what `vxlanmgrd` emits to APP_DB; only
a working end-to-end CLI test tells you all of those simultaneously.

The anti-pattern: read the SONiC schema, guess the CONFIG_DB entries,
write them directly into newtron, then debug failures from daemon logs.
This approach leads to hours of investigation for issues that a
five-minute CLI test would have caught. The daemon logs tell you *that*
something failed; only the CLI path shows you *what* the correct
sequence is.

**Never assume a CONFIG_DB path works without first verifying it via CLI
on a real device.**

---

## 20. Documentation Freshness — Audit, Don't Grep

When updating documentation after schema or API changes — renamed fields,
new types, changed CLI flags — targeted grep alone gives false confidence.
The problem is that grep finds the patterns you already know are wrong.
It misses prose descriptions that use different wording, glossary tables
with stale entries, Go code examples that embed old field names,
incomplete flag lists, and contradictory statements where one section
says the old name and another says the new name.

The protocol has three steps:

1. **Initial grep pass.** Fix the obvious stale references — the ones
   you know to look for. This catches the bulk of the changes quickly.

2. **Full-file audit.** After the grep-based fixes, read the entire
   document end-to-end. Check every reference against the ground truth
   schema — the complete, authoritative list of all field names, CLI
   flags, type values, and table names. The audit must be provided with
   the complete ground truth, not just the patterns that changed, because
   staleness appears in forms you didn't anticipate.

3. **One commit, fully clean.** Fix everything the audit finds before
   committing. A documentation update that requires a follow-up fix is
   a documentation update that wasn't finished.

This was learned the hard way: a documentation update that grep'd for
four known stale patterns shipped with twelve remaining stale references
that a full-file audit caught afterward. The grep pass gave confidence
that the job was done; the audit proved it wasn't. The grep found what
was already known to be wrong; the audit found what wasn't known to be
wrong.

The principle applies beyond documentation: any verification based on
searching for known bad patterns inherits the same blind spot. The
patterns you search for are the patterns you already know about.

**Grep finds what you already know is wrong; audits find what you don't
know is wrong.**

---

## 21. Pure Config Functions — Separate Generation from Orchestration

Entry construction for each CONFIG_DB table is extracted into **pure config
functions** — functions that take CONFIG_DB state and identity parameters,
return `[]sonic.Entry`, and have no side effects. They don't check
preconditions, don't build ChangeSets, don't log, and don't connect to
devices. They answer one question: "what entries does this operation
produce?"

Config functions come in three forms:

- **Package-level functions** for stateless entry construction where the
  subject is not an interface: `createVlanConfig(vlanID, ...)`,
  `createVrfConfig(vrfName)`, `CreateBGPNeighborConfig(peerIP, ...)`.
  These take only the values needed to construct the entry — no device
  state.
- **Node methods** for config-scanning functions that need to read
  ConfigDB to determine what to produce: `n.destroyVlanConfig(vlanID)`,
  `n.destroyVrfConfig(vrfName, l3vni)`, `n.unbindIpvpnConfig(vrfName)`,
  `n.deleteAclTableConfig(name)`. These scan `n.configDB` to find
  dependent entries (members, VNI mappings, route targets) for cascading
  teardown. Making them Node methods ensures they always scan the correct
  device's state. See Principle 26.
- **Interface methods** for tables where the interface is the subject:
  `i.bindVrf(vrfName)`, `i.assignIpAddress(ipAddr)`, `i.bindQos(...)`,
  `i.generateAclBinding(...)`, `i.generateServiceEntries(...)`. These use
  `i.name` and `i.node` (for SpecProvider/ConfigDB access), eliminating
  the interface name parameter that a free function would require. See
  Principle 24.

```go
// Package-level config function — stateless, owned by vlan_ops.go
func createVlanConfig(vlanID int, opts VLANConfig) []sonic.Entry

// Node method config function — scans configDB, owned by vlan_ops.go
func (n *Node) destroyVlanConfig(vlanID int) []sonic.Entry

// Interface method config function — owned by interface_ops.go
func (i *Interface) bindVrf(vrfName string) []sonic.Entry
```

Operations call these functions and wrap the result:

```go
// Simple CRUD — uses the op() helper
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, ...) (*ChangeSet, error) {
    return n.op("create-vlan", vlanName, ChangeAdd,
        func(pc *PreconditionChecker) { pc.RequireVLANNotExists(vlanID) },
        func() []sonic.Entry { return createVlanConfig(vlanID, opts) },
    )
}
```

The `op()` helper handles the common pattern: run preconditions → call
generator → wrap in ChangeSet. Complex operations (ApplyService,
RemoveService, SetupEVPN) that need custom logic between steps build their
ChangeSets directly but still call config functions for entry construction.

`sonic.Entry` is the unified entry type used everywhere — config functions,
composite builders, and pipeline delivery. It replaced the former separate
`CompositeEntry` and `TableChange` types, eliminating unnecessary
conversions between structurally identical representations.

This layering serves three purposes:

1. **Testability.** Config functions can be unit-tested with a fake ConfigDB
   — no connections, locks, or preconditions. The function's output is
   deterministic given its inputs.

2. **Reuse.** The same config function is called by the online operation
   path (Interface.ApplyService → ChangeSet), the offline composite path
   (TopologyProvisioner → CompositeBuilder), and delete operations.
   Change the table format once; all paths update.

3. **Clarity.** A reader can open `vlan_ops.go` and see exactly what
   entries a VLAN operation produces, without wading through precondition
   checks, logging, and ChangeSet bookkeeping.

**Generate entries in pure functions; orchestrate them in operations.**

---

## 22. On-Demand Interface State — No Cached Fields

The Interface struct contains exactly two fields: a parent pointer (`node`)
and the interface name. All property accessors — `AdminStatus()`, `VRF()`,
`IPAddresses()`, `ServiceName()`, etc. — read on demand from the Node's
ConfigDB and StateDB snapshots, which are refreshed at Lock time.

There is no `loadState()` function. There are no cached private fields that
operations must remember to update after mutations. An accessor called
before and after a CONFIG_DB write within the same episode returns the
same value (the snapshot is immutable within an episode); after the next
Lock/Refresh, it reflects the new state.

This eliminates a class of bugs where an operation mutates CONFIG_DB (via
`cs.Apply()`) but forgets to update a cached Interface field, causing
subsequent reads within the same session to see stale data. The previous
design had 15 cached fields that required careful synchronization; the
on-demand design has zero.

The trade-off is clear: on-demand reads are marginally slower (a map
lookup per call vs. a field read). But Interface accessors are called
dozens of times per operation, not millions — the overhead is unmeasurable
against the Redis round-trip cost of any real operation. Correctness
matters more than nanoseconds.

**Read state from the snapshot, not from cached fields. Snapshots are
refreshed per episode; fields go stale within one.**

---

## 23. Symmetric Operations — What You Create, You Can Remove

Every mutating operation in newtron must have a corresponding reverse
operation. If newtron can create a VRF, it must be able to delete that
VRF. If it can apply a service, it must be able to remove that service.
If it can bind an ACL to an interface, it must be able to unbind it. No
CONFIG_DB state created by newtron should require a human with `redis-cli`
or `config` commands to clean up.

This is not just an API completeness requirement — it is a correctness
requirement. CONFIG_DB entries have dependencies: a VRF references
interfaces, a VLAN references members, an ACL references bound ports.
Deletion must understand these dependencies just as creation does. A
`DeleteVLAN` that leaves orphaned `VLAN_MEMBER` entries is worse than no
delete at all, because the orphaned entries cause silent failures in
SONiC daemons.

The symmetry extends to composite operations. `SetupEVPN` creates the
VTEP, NVO, and tunnel map entries; `TeardownEVPN` removes all of them.
`ApplyService` creates VRFs, VLANs, ACLs, BGP neighbors, and a service
binding; `RemoveService` reads the binding and removes everything that
was created, respecting shared resources via `DependencyChecker`.

The current operation pairs:

| Create | Remove |
|--------|--------|
| `CreateVLAN` | `DeleteVLAN` |
| `AddVLANMember` | `RemoveVLANMember` |
| `ConfigureSVI` | `RemoveSVI` |
| `CreateVRF` | `DeleteVRF` |
| `AddVRFInterface` | `RemoveVRFInterface` |
| `AddStaticRoute` | `RemoveStaticRoute` |
| `BindIPVPN` | `UnbindIPVPN` |
| `CreatePortChannel` | `DeletePortChannel` |
| `AddPortChannelMember` | `RemovePortChannelMember` |
| `CreateACLTable` | `DeleteACLTable` |
| `AddACLRule` | `DeleteACLRule` |
| `SetupEVPN` | `TeardownEVPN` |
| `MapL2VNI` | `UnmapL2VNI` |
| `ApplyService` | `RemoveService` |
| `ApplyQoS` | `RemoveQoS` |
| `SetIP` | `RemoveIP` |
| `AddBGPNeighbor` | `RemoveBGPNeighbor` |
| `BindACL` | `UnbindACL` |

When adding a new operation that creates CONFIG_DB state, the
corresponding removal operation is not optional — it is part of the
feature. Ship both or ship neither.

This extends to config generator functions — the pure functions that
return `[]sonic.Entry`. Every forward generator must have a reverse:

| Forward verb | Reverse verb | Example |
|-------------|-------------|---------|
| `create*Config` | `delete*Config` or `destroy*Config` | `createVlanConfig` / `deleteVlanConfig` / `n.destroyVlanConfig` |
| `enable*Config` | `disable*Config` | `enableArpSuppressionConfig` / `disableArpSuppressionConfig` |
| `bind*Config` | `unbind*Config` | `bindIpvpnConfig` / `n.unbindIpvpnConfig` |
| `assign*` | `unassign*` or `remove*` | `i.assignIpAddress` / removal via same key |
| `Add*` | `Remove*` or `Delete*` | `AddVLANMember` / `RemoveVLANMember` |

`destroy*` is reserved for cascading teardowns that scan ConfigDB for
dependent entries (members, VNI mappings, etc.) and remove them all.
Simple `delete*` removes a single entry.

When adding a new forward config generator, the reverse must be added
in the same commit. When reviewing code, verify that `RemoveService`
and other teardown paths clean up every entry that the forward path
creates.

**If newtron creates it, newtron must be able to remove it. No orphans,
no manual cleanup, no `redis-cli` required.**

### Shared Resources and Safe Reversal

CONFIG_DB resources are often shared. A VRF serves multiple services.
A filter binds to multiple interfaces. A QoS policy applies to several
ports. Forward operations handle sharing via idempotency — `ApplyService`
checks whether the VRF already exists before creating it, so the second
service that uses the same VRF simply reuses it.

Reverse operations must be equally aware. `RemoveService` cannot blindly
delete the VRF it finds in the service binding — another service may still
depend on it. This is why every removal path uses `DependencyChecker` to
scan CONFIG_DB for remaining consumers before deleting shared resources.

This has a critical architectural implication: **mechanical ChangeSet
reversal is unsafe.** A ChangeSet records low-level CONFIG_DB mutations
(HSET, DEL) but not the sharing context in which they were made. Reversing
those mutations would delete a VRF that two services share, remove a filter
still bound to another interface, or tear down a VTEP that other overlays
depend on. Only domain-level reverse operations (`RemoveService`,
`UnbindACL`, `RemoveQoS`) have the context to safely determine whether a
shared resource can be removed.

Rollback is therefore an orchestrator concern, not a newtron concern. If
an orchestrator provisions three interfaces and the second fails, it calls
`RemoveService` on the first — not "reverse the first ChangeSet." newtron
provides safe, reference-aware building blocks; the orchestrator decides
when to invoke them.

### Never Enter a State You Can't Recover From

Symmetric operations guarantee that every forward action has a reverse.
But the reverse only helps if you can find what needs reversing. If a
process crashes mid-apply, the in-memory ChangeSet is lost.

The intent record (`NEWTRON_INTENT|<device>`) records the operation list
before applying, so crash recovery can call the reverse of each. But
this only works if the record is written. If the intent write fails and
the operation proceeds, crash recovery is impossible — partial CONFIG_DB
writes with no record of what was attempted.

**If the safety net cannot be established, do not create a situation
that needs one.** A failed intent write aborts the operation. Proceeding
without the intent is proceeding with the assumption that nothing will
go wrong — exactly the assumption that crash recovery exists to
challenge.

### Structural Proof over Heuristic Detection

When detecting whether a previous operation left orphaned state, use
structural facts rather than heuristic thresholds.

The intent record lifecycle is: WriteIntent → Apply → DeleteIntent →
Unlock. If a new process acquires the lock and an intent exists, the
previous process crashed between WriteIntent and DeleteIntent. Lock
acquisition is the proof — no staleness timer, no TTL comparison, no
edge cases.

This pattern generalizes: anywhere a heuristic (timeout, polling
interval, retry count) detects a condition, ask whether a structural
fact already proves it. A lock that was acquired proves the previous
holder is gone. Structural proofs are binary (true or false). Heuristics
have thresholds, and thresholds have edge cases.

---

## 24. Respect Abstraction Boundaries

When an abstraction exists — Interface, Node — callers must use it.
They must not bypass it by calling lower-level functions that expose
internal schema or require passing identity the object already knows.

This principle is the structural enforcement of Principle 4 (Objects Own
Their Methods). Principle 4 says *where* methods belong; this principle
says *callers must respect those boundaries*.

**Rule 1: If an operation is scoped to an interface, it is a method on
Interface.** The Interface knows its own name — requiring callers to pass
it is an abstraction leak. A function `interfaceVRFConfig(intfName, vrfName)`
forces every caller to supply `intfName` — but the Interface already has
it. The correct form is `i.bindVrf(vrfName)`.

Exception: container membership operations (VLAN members, PortChannel
members) where the container is the subject — `createVlanMemberConfig(vlanID,
intfName, tagged)` is correct because the VLAN is the subject, not the
interface.

**Rule 2: Config methods belong to the object they describe.**
`i.bindVrf(vrfName)` not `interfaceVRFConfig(intfName, vrfName)`.
The object provides its own identity; callers express intent, not identity.

**Rule 3: Node convenience methods delegate, not duplicate.**
`Node.ApplyQoS(intfName, ...)` resolves a name string to an Interface
and calls `iface.ApplyQoS(...)`. It never re-implements the operation.
The same applies to `Node.RemoveQoS`, `Node.AddVRFInterface`, etc.

**Rule 4: No "absolute blocker" for `i.node` access.** Interface methods
that need ConfigDB or SpecProvider use `i.node.ConfigDB()` or `i.node`
(SpecProvider). Needing external data is not a reason to avoid being a
method — it's the reason the parent pointer exists.

The current Interface methods:

| Method | Purpose |
|--------|---------|
| `ApplyService()` | Full service lifecycle — apply |
| `RemoveService()` | Full service lifecycle — remove |
| `ApplyQoS()` | QoS lifecycle — apply |
| `RemoveQoS()` | QoS lifecycle — remove |
| `SetIP()` | Assign IP address |
| `SetVRF()` | Bind/unbind VRF |
| `BindACL()` | Bind ACL to this interface |
| `UnbindACL()` | Unbind ACL from this interface |
| `generateServiceEntries()` | Config: full service entries |
| `bindVrf()` | Config: VRF binding entry |
| `enableIpRouting()` | Config: enable IP routing (empty INTERFACE entry) |
| `assignIpAddress()` | Config: IP address entry |
| `bindQos()` | Config: QoS per-interface entries |
| `unbindQos()` | Config: QoS removal entries |
| `generateAclBinding()` | Config: ACL table + rules |

**Abstractions exist to be used, not bypassed. If an object knows its
own identity, callers must not re-supply it.**

---

## 25. Verb-First, Domain-Intent Naming

Two rules govern function naming:

**Rule 1: Verbs come first.** Functions that describe actions put the
verb before the noun. This follows Go standard library convention
(`os.Remove`, `json.Marshal`, `strings.Split`) and makes code read as
imperative statements:

```go
// Correct — verb first
createVlanConfig(vlanID, opts)
n.destroyVrfConfig(vrfName, l3vni)
enableArpSuppressionConfig(vlanName)
bindIpvpnConfig(vrfName, ipvpnDef, ...)
deleteVlanMemberConfig(vlanID, intfName)
i.bindVrf(vrfName)
i.assignIpAddress(ipAddr)
i.enableIpRouting()

// Wrong — noun first
vlanCreate(vlanID, opts)
vrfDestroy(vrfName, l3vni)
arpSuppressionEnable(vlanName)
ipvpnBind(vrfName, ipvpnDef, ...)
vlanMemberDelete(vlanID, intfName)
i.vrfBinding(vrfName)
i.ipAddress(ipAddr)
i.ipRoutingEnable()
```

The verb vocabulary for config generators:

| Verb | Meaning | Example |
|------|---------|---------|
| `create` | Construct entries for a new entity | `createVlanConfig`, `createVrfConfig`, `CreateBGPNeighborConfig` |
| `delete` | Remove a single entity's entries | `deleteVlanConfig`, `deleteVlanMemberConfig` |
| `destroy` | Cascading teardown (Node method, scans configDB) | `n.destroyVlanConfig`, `n.destroyVrfConfig` |
| `enable`/`disable` | Toggle a behavior | `enableArpSuppressionConfig`, `disableArpSuppressionConfig` |
| `bind`/`unbind` | Establish/remove a relationship | `bindIpvpnConfig`, `n.unbindIpvpnConfig`, `i.bindVrf` |
| `assign`/`unassign` | Attach/detach a value | `i.assignIpAddress` |
| `update` | Modify fields on an existing entry | `updateDeviceMetadataConfig` |
| `generate` | Composite entry generation | `i.generateServiceEntries`, `generateBGPPeeringConfig` |

Noun-only names are reserved for types, constructors, and key helpers —
never for functions that describe actions.

**Rule 2: Names describe domain intent, not CONFIG_DB mechanics.**
"Sub" and "Entry" are implementation concepts. A network engineer should
understand what a function does without knowing CONFIG_DB table names:

```go
i.bindVrf(vrfName)                      // "bind this interface to a VRF"
i.enableIpRouting()                     // "enable IP routing on this interface"
i.assignIpAddress(ipAddr)               // "assign an IP address"
i.bindQos(policyName, policy)           // "bind QoS policy to this interface"
i.unbindQos()                           // "unbind QoS from this interface"
i.generateAclBinding(svc, filter, stage) // "generate ACL binding for this interface"
i.generateServiceEntries(params)        // "generate all service entries"
canBridge / canRoute                    // capability, not layer number
```

- **Methods on the subject**: `i.bindVrf()` reads as "bind VRF to this
  interface" — the verb plus the receiver tells the whole story
- **Parameters express intent**: `bindVrf(vrfName)` says "bind to this
  VRF". `interfaceBaseConfig(intfName, map[string]string{"vrf_name":
  vrfName})` says "write these fields to the INTERFACE table for this
  name" — implementation detail, not intent
- **Capability over layer**: `canBridge`/`canRoute` describe what a
  service can do. `hasL2`/`hasL3` describe which OSI layer it uses.
  Exception: `L2VNI`/`L3VNI` are standard EVPN RFC terms.

**Verbs first; domain language always; implementation terms never.**

---

## 26. Node as Device Isolation Boundary

The Node is the device isolation boundary. Every `*Node` instance owns
its own `configDB`, Redis connection, interface map, and resolved specs.
All device-scoped operations — config scanning, precondition checking,
dependency analysis — are methods on Node, ensuring they always operate
on the correct device's state.

This is designed for a multi-device future. When newtron operates on
multiple devices simultaneously — whether for coordinated provisioning,
cross-device validation, or fabric-wide operations — each Node is fully
self-contained:

```go
node1, _ := network.ConnectNode(ctx, "switch1")
node2, _ := network.ConnectNode(ctx, "switch2")

// Each interface knows its parent Node
iface1, _ := node1.GetInterface("Ethernet0")  // switch1's Ethernet0
iface2, _ := node2.GetInterface("Ethernet0")  // switch2's Ethernet0

// Operations are device-scoped by construction
cs1, _ := iface1.ApplyService(ctx, "transit", opts)  // scans switch1's configDB
cs2, _ := iface2.ApplyService(ctx, "transit", opts)  // scans switch2's configDB
```

The key architectural choice: **config-scanning functions are Node methods,
not free functions that take `configDB` as a parameter.** A free function
like `destroyVrf(configDB, vrfName, l3vni)` requires the caller to pass
the correct `configDB` — in a multi-device context, passing the wrong one
is a silent bug that scans the wrong device's state. A Node method like
`n.destroyVrfConfig(vrfName, l3vni)` eliminates this class of bug
structurally: the method always operates on `n.configDB`.

The same principle applies transitively through the object hierarchy:

- **`DependencyChecker`** takes a `*Node` and accesses `dc.node.ConfigDB()`
  — it's device-scoped by construction.
- **Interface methods** reach `i.node` for device state — no possibility
  of cross-device contamination between `switch1/Ethernet0` and
  `switch2/Ethernet0`.
- **`SpecProvider`** is per-Node — each device sees its own resolved specs
  (different profiles, different platforms, different overrides).

No shared mutable state crosses the Node boundary. A multi-device
orchestrator is purely an iteration concern — loop over Nodes, call
self-contained methods on each. The Node provides the isolation; the
orchestrator provides the coordination.

**Every device-scoped operation is a Node method. Cross-device
coordination belongs in the orchestrator, not in the Node.**

---

## 27. Abstract Node — Same Code Path, Different Initialization

The Node operates in two modes:

- **Physical mode** (`offline=false`): ConfigDB loaded from Redis at
  Connect/Lock time. Preconditions enforce connected+locked. Each
  ChangeSet is applied to Redis immediately.
- **Abstract mode** (`offline=true`): Shadow ConfigDB starts empty.
  Operations build desired state against the shadow. Entries accumulate
  for composite export via `BuildComposite()`.

Same code path, different initialization. The topology provisioner creates
an abstract Node and calls the same methods the CLI uses:

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
inline — it calls the same Interface and Node methods, and the shadow
enforces the same ordering constraints that a physical device would.

### Why the shadow matters

Each operation's output must be visible to subsequent operations'
preconditions. `applyShadow(cs)` updates the shadow ConfigDB after every
operation so that:

- `CreateVRF("CUSTOMER")` → shadow now has `VRF|CUSTOMER`
- `iface.SetVRF(ctx, "CUSTOMER")` → precondition `VRFExists` passes ✓
- `SetupEVPN(ctx, ...)` → shadow now has `VXLAN_TUNNEL`
- `iface.ApplyService(ctx, ...)` → precondition `VTEPConfigured` passes ✓

Without the shadow, the abstract node would have no state for
preconditions to check — every operation would either fail or require
precondition checks to be skipped, losing the correctness guarantees.

### Provisioning vs operations

This design reflects a fundamental distinction:

- **Provisioning** (`CompositeOverwrite`): intent replaces reality.
  The abstract node builds the complete desired state, then merges it
  on top of CONFIG_DB — removing stale keys while preserving factory
  defaults. This is the one case where we know the full picture.
- **Operations** (`ChangeSet.Apply`): mutations against existing reality.
  The physical node reads current state from Redis, applies a delta,
  and verifies the result.

The abstract node exists to serve provisioning. The physical node exists
to serve operations. Both use the same methods — only initialization and
delivery differ.

**Same code path, different initialization. The Interface is the point of
service in both modes.**

---

## 28. Public API Boundary — Types Express Intent, Not Implementation

`pkg/newtron/` is the public API. `pkg/newtron/network/`,
`pkg/newtron/network/node/`, and `pkg/newtron/device/sonic/` are internal
implementation packages. All external consumers — CLI, newtrun, the newtron-server HTTP
server — import only `pkg/newtron/`.

This boundary is not just an import path convention. It is a type boundary
that separates what callers need from what the implementation needs. Public
types use domain vocabulary: `RouteNextHop.Address` (a network address),
`CompositeInfo.Tables` (what was provisioned), `WriteResult.ChangeCount`
(what happened). Internal types reflect implementation structure:
`NextHop.IP` (a Redis field), `CompositeConfig.Entries` (a delivery
format), `ChangeSet.Changes` (a list of Redis commands). The boundary
conversion between public and internal types strips implementation details
and maps to domain names.

This distinction emerged from a concrete migration. newtrun — the E2E test
orchestrator — originally imported three internal packages directly. It
constructed `node.ChangeSet` objects, resolved specs via
`network.GetIPVPN()`, accessed `sonic.RouteEntry` structs, and called
`dev.ConfigDBClient().Get()` for verification. Every internal refactor
(renamed fields, restructured types, changed method signatures) broke
newtrun. The orchestrator was coupled to implementation, not intent.

After migrating newtrun to use only the public API, five rules crystallized.
These rules were subsequently validated by the newtron-server HTTP server,
where every handler calls `pkg/newtron/` methods directly — the clean
public API boundary is what makes the transport layer transparent
(Principle 29):

**Rule 1: Orchestrators are API consumers, not insiders.** If an
orchestrator needs functionality the API doesn't expose, extend the API —
don't bypass it with internal imports. The CLI, newtrun, and
newtron-server all consume `pkg/newtron/` without reaching into internal
packages.

**Rule 2: Operations accept names; the API resolves specs.** Callers pass
`ipvpnName`, `macvpnName`, `policyName` — string identifiers of intent.
The API method resolves the spec from the Node's SpecProvider internally.
Callers never pre-resolve specs and pass structs across the boundary. This
means spec format changes are invisible to API consumers: `BindIPVPN(ctx,
vrfName, ipvpnName)` works regardless of how `IPVPNSpec` is structured
internally.

**Rule 3: Verification tokens are opaque.** `CompositeInfo` flows from
`GenerateDeviceComposite()` to `VerifyComposite()` as an opaque handle.
Callers store it, pass it back, and read the result. They never inspect
internal state — the verification mechanism (ChangeSet diffing against
CONFIG_DB) is entirely hidden. This allows the verification implementation
to change (different diff strategies, additional checks) without affecting
any consumer. At a network boundary (HTTP), opaque handles that contain
non-serializable state become server-side stores with client-facing UUID
tokens — the opacity is preserved, the mechanism extends naturally.

**Rule 4: Write results report outcomes, not internals.**
`WriteResult.ChangeCount` and `DeliveryResult.Applied` tell callers what
happened. Raw ChangeSets, Redis commands, and CONFIG_DB key formats never
cross the boundary. An orchestrator that logs "created VLAN 100 (3
changes)" does not need to know that those 3 changes were `HSET
VLAN|Vlan100`, `HSET VLAN_MEMBER|Vlan100|Ethernet4`, and `HSET
VLAN_INTERFACE|Vlan100`.

**Rule 5: Public types are domain types, not wrappers.** A public type is
not a thin wrapper around an internal type with the same fields. It is a
domain-vocabulary type designed for what callers need to know. Internal
`sonic.NextHop` has `IP` (a Redis field name); public `RouteNextHop` has
`Address` (what a network engineer calls it). Internal `node.ChangeSet`
exposes `Changes []ConfigChange` with Redis operations; public
`WriteResult` exposes `ChangeCount int` because callers need the outcome,
not the operations.

When adding new public API surface:

1. Define the public type in `types.go` using domain vocabulary
2. Add the method to the appropriate wrapper (`Network`, `Node`,
   `Interface`)
3. Convert internal types at the boundary — never expose `*node.X` or
   `*sonic.Y` directly
4. Accept names/strings for spec references; resolve internally
5. Return outcome structs, not implementation structs

**Public types expose what callers need; internal types expose what the
implementation needs. The boundary conversion strips and maps as needed.**

### Uniform Boundaries — If It Exists, It Exists Everywhere

A structural boundary, once justified, applies to every type that crosses
it. If `RouteEntry` needs a conversion function because `NextHop.IP`
becomes `RouteNextHop.Address`, then `VerificationResult`,
`OperationIntent`, and `NeighEntry` also get conversion functions — even
when their fields happen to be identical today. The boundary is a property
of the architecture, not a per-type decision.

The alternative — type aliases for "simple" types, conversion functions
for "complex" ones — creates an inconsistent boundary. A reader cannot
predict which pattern a given type follows. Worse, when a type that was
aliased later needs a vocabulary change, the alias must be replaced with
a conversion function, changing every callsite that relied on the types
being identical. The uniform boundary absorbs this change: the conversion
function already exists, only its internals change.

This is the same principle as operational symmetry (§23) applied to the
type system. Operational symmetry says: if you can create, you must be
able to remove — even if removal is trivial today. Uniform boundaries
say: if one type needs boundary conversion, all types at that boundary
get boundary conversion — even if the conversion is trivial today. Both
resist the temptation to skip structure because the current case is
simple. Simple cases become complex; the structure must already be there
when they do.

---

## 29. Transparent Transport — The Middle Layer Has No Logic

The HTTP server layer (`pkg/newtron/api/`) is a mechanical translation
between wire format and public API method calls. Every handler follows
the same pattern:

1. Decode JSON request body
2. Construct a closure that calls a `pkg/newtron/` method
3. Send the closure to an actor for serialized execution
4. Encode the result as JSON

There is no business logic in the transport layer. Domain validation,
precondition checks, error classification, and state management all live
inside `pkg/newtron/`. The handler is the glue — there is nothing else
in the middle.

### Why This Works

newtron operations are gated by slow downstream I/O: SSH connections,
Redis round-trips, and device response times measured in hundreds of
milliseconds to seconds. The transport layer contributes nanoseconds.
Optimizing it — typed dispatch infrastructure, binary protocols,
zero-copy serialization — would be optimizing a layer that is invisible
against the downstream cost.

The alternative — typed message structs for each of 80+ operations,
dispatch routing tables, intermediate representation layers — creates
coordination points. Each new endpoint requires changes in three or more
places (message type, routing entry, handler). When these drift, you get
silent routing errors, type mismatches, or "added the handler but forgot
to register the message type" bugs. The more boilerplate in the middle,
the more places for drift, the more brittle the system.

### Closures Over Typed Messages

Generic closures (`func() (any, error)`) make the actor infrastructure
O(1). Each closure captures decoded parameters by value — no shared
mutable state between the HTTP goroutine and the actor goroutine. This
achieves the same safety guarantees as typed messages with zero
per-operation boilerplate.

Adding a new API endpoint requires exactly one handler function. No new
message types, no dispatch table entries, no middleware changes, no actor
modifications.

### Actor Serialization

NetworkActors and NodeActors are goroutines that read closures from a
channel and execute them sequentially. This serializes access to mutable
resources (Network spec maps, Node device connections) without mutexes.

Why actors over mutexes: spec writes do file I/O and modify in-memory
maps; Node operations do SSH+Redis. These are long-held operations —
holding a mutex across an SSH round-trip blocks all other goroutines
waiting on that resource. An actor naturally queues concurrent requests
and processes them one at a time, which is exactly the behavior needed
when operations are slow and must not interleave.

NodeActors provide serialization and connection caching. SSH connections
are reused across requests within a configurable idle timeout (default
5 minutes), eliminating the ~200ms connection overhead per request. But
the episodic caching principle (P18) still holds: every request refreshes
CONFIG_DB from Redis (`Lock()` for writes, `Refresh()` for reads) so
operations always see current device reality. The SSH tunnel is reused;
the device state is never assumed from a prior request.

### The Tradeoff

This architecture is correct for I/O-gated systems. For
high-performance systems where transport latency matters (microsecond
targets, millions of ops/sec), the tradeoff inverts: accept
typed-message boilerplate to minimize allocation and dispatch overhead,
and minimize API surface area because each endpoint has real cost. The
decision criterion is simple: where is the bottleneck? If the
downstream is 1000x slower than the transport, make the transport
transparent. If the transport is the bottleneck, invest in its
performance.

**The transport layer is a mechanical shim. Domain logic lives in the
public API. New endpoints are additive — one handler function, zero
infrastructure changes.**

---

## 30. YANG-Derived Schema Validation — Trust the Spec, Not the Developer

CONFIG_DB accepts anything. Write `VLAN|Vlan99999` with `vlanid: -7` and
Redis stores it without complaint. SONiC daemons downstream — orchagent,
vlanmgrd, bgpcfgd — respond to these entries with silent failures,
crashes, or undefined behavior. The problem is not malice; it's typos,
off-by-one ranges, and developers writing field names from memory instead
of checking the YANG model.

newtron solves this with a static schema table in
`pkg/newtron/device/sonic/schema.go` that encodes per-table, per-field
constraints derived from the SONiC YANG models. Every ChangeSet passes
through `Validate()` before any Redis write:

```go
func (cs *ChangeSet) Apply(n *Node) error {
    // ... precondition checks (business logic: does the resource exist?) ...
    if err := cs.Validate(); err != nil {
        return err  // schema violations → no writes at all
    }
    // ... Redis writes ...
}
```

This is a different layer from preconditions. Preconditions enforce
business logic ("does this VRF exist?", "is this interface already
bound?"). Schema validation enforces data format ("is this ASN in the
range 1–4294967295?", "is this a valid MAC address?", "does this table
even exist?").

The schema is **fail-closed**:

- **Unknown table → error.** Every table newtron writes must have a
  schema entry. A developer who adds a new `configDB.Set("NEW_TABLE", ...)`
  in `*_ops.go` will see validation failures until they add `NEW_TABLE`
  to `schema.go`.

- **Unknown field → error.** Every field newtron writes must be declared.
  This catches misspelled field names at the point of write, not when
  a daemon silently ignores the entry 30 seconds later.

- **Deletes validate key format only.** A delete doesn't need field
  validation — it's removing data, not writing it. But the key must
  still match the expected pattern.

The constraints come from SONiC YANG models, stored as a reference in
`pkg/newtron/device/sonic/yang/constraints.md`. Each constraint in
`schema.go` carries a `// YANG:` comment citing its source. When newtron
diverges from YANG (e.g., writing `nexthop_unchanged` instead of YANG's
`unchanged_nexthop`), the deviation is documented.

The maintenance contract:

1. **Upgrading SONiC buildimage** → re-fetch YANG files, diff against
   `constraints.md`, update `schema.go` for any changed ranges, enums,
   or field names.
2. **Adding a new CONFIG_DB table** → fetch its YANG model, extract
   constraints, add to both `constraints.md` and `schema.go`.
3. **A daemon rejects a CONFIG_DB entry that passed validation** → the
   constraint table is missing or wrong; fix it.

**YANG is the authority. The developer's intuition about what values are
valid is not. When schema.go and the YANG model disagree, the YANG model
wins — unless there is a documented, justified deviation.**

---

## 31. Write Ordering and Daemon Settling — Respect the Dependency Chain

CONFIG_DB is a flat key-value store with no referential integrity. Redis
accepts entries in any order. But the daemons that consume CONFIG_DB
entries — orchagent, vlanmgrd, vrfmgrd, intfmgrd, bgpcfgd, frrcfgd —
have implicit ordering requirements derived from the YANG leafref chain.
Writing entries out of order doesn't cause a Redis error; it causes a
daemon to silently ignore an entry, crash, or enter an unrecoverable
state.

### The Dependency Chain

YANG `leafref` declarations define a directed dependency graph. A table
with a leafref to another table cannot be meaningfully processed until
the referenced entry exists. The critical chains for newtron:

```
VLAN  ──→  VLAN_MEMBER  ──→  (interface must exist)
VLAN  ──→  VLAN_INTERFACE  ──→  VRF (via vrf_name leafref)
VRF   ──→  BGP_GLOBALS  ──→  BGP_NEIGHBOR  ──→  BGP_NEIGHBOR_AF
VRF   ──→  INTERFACE (via vrf_name)
VRF   ──→  BGP_GLOBALS_AF  ──→  ROUTE_REDISTRIBUTE
VXLAN_TUNNEL  ──→  VXLAN_EVPN_NVO  ──→  VXLAN_TUNNEL_MAP
ACL_TABLE  ──→  ACL_RULE
SCHEDULER  ──→  QUEUE (via bracket-ref)
DSCP_TO_TC_MAP  ──→  PORT_QOS_MAP (via bracket-ref)
```

Every config function in newtron returns entries in dependency order.
`service_gen.go` documents this explicitly: "returned slice is ordered
by table dependency (VLANs before members, VRFs before interfaces,
etc.)." Forward operations append parents before children; reverse
operations delete children before parents.

### Structural Ordering, Not Timing Hacks

Write ordering is enforced structurally — by the order entries appear in
the slice returned by config functions — not by inserting `time.Sleep`
between writes. This is deliberate:

- Config functions return `[]sonic.Entry` in dependency order. Callers
  `append()` these slices in the correct sequence.
- `PipelineSet` sends entries to Redis in slice order via MULTI/EXEC.
- The ChangeSet `Apply()` loop iterates changes sequentially.

There are no `time.Sleep` calls in the write path. If a developer feels
the need to add a sleep between CONFIG_DB writes, it means the ordering
is wrong or the daemon has a bug — both of which deserve investigation,
not a timing band-aid.

### Daemon Settling Time

Redis accepts entries instantly, but daemons need time to process them.
When a MULTI/EXEC transaction commits hundreds of entries atomically,
every subscribed daemon receives a burst of keyspace notifications
simultaneously. Processing delays vary by daemon and platform:

| Daemon | Operation | Typical Latency | Platform Notes |
|--------|-----------|-----------------|----------------|
| vrfmgrd | VRF → kernel netdev | <1s (VPP), 1–5s (CiscoVS) | Writes STATE_DB only on startup path (RCA-041) |
| intfmgrd | Interface VRF binding | 1–30s (CiscoVS) | Must wait for vrfmgrd to create kernel device (RCA-037) |
| orchagent | VLAN/VRF/EVPN → SAI | 60–90s (CiscoVS) | Silicon One simulator latency; VPP is faster |
| bgpcfgd | BGP config → FRR | <1s | Except ASN changes: requires daemon restart (RCA-019) |
| frrcfgd | VRF VNI → FRR | 1–2s polling | vrf_handler pub/sub broken on CiscoVS; polling fallback |

These latencies matter in two contexts:

1. **Post-provisioning convergence.** After `DeliverComposite` writes
   the full device config, daemons need time to process everything
   before the device is operational. newtrun test suites handle this
   with polling-based health checks (`pollUntil` with configurable
   timeout and interval), not hardcoded sleeps.

2. **Inter-daemon races.** When two daemons process related entries
   from the same atomic commit, one may finish before the other has
   created a prerequisite. RCA-037 documents this: intfmgrd tries to
   bind an interface to a VRF before vrfmgrd has created the kernel
   VRF device. The fix is a post-provisioning polling step that
   verifies kernel state, not a sleep between CONFIG_DB writes.

### Known Race Conditions

Three documented races establish the patterns:

**RCA-037 (VRF binding race):** VLAN + VRF provisioned together →
intfmgrd processes the INTERFACE entry before vrfmgrd creates the
kernel VRF device. Race window: 1–30 seconds on CiscoVS. Fix:
post-provisioning polling step that checks `ip link show type vrf`
and forces the binding if needed.

**RCA-041 (VRF_TABLE asymmetry):** vrfmgrd writes `VRF_OBJECT_TABLE`
on runtime notification but only writes `VRF_TABLE` during its startup
code path. intfmgrd checks `VRF_TABLE` → not found → retries forever.
Fix: use `config reload` (which restarts all daemons, triggering the
startup path) instead of selective daemon restarts after provisioning.

**RCA-016 (BGP route staleness):** After parallel device provisioning,
BGP sessions establish but routes aren't exchanged. Fix: two-stage BGP
clear — per-device soft clear (both directions) immediately after
provision, then a global refresh 5 seconds after all devices are
provisioned.

### The Principle

Write ordering is a compile-time property: config functions encode it
in the slice they return. Daemon settling is a runtime property: test
suites verify it with polling, not sleeps.

**When adding a new CONFIG_DB table:**

1. Identify its YANG leafref dependencies — what must exist before it.
2. Place its entries after the dependency in the config function's
   return slice.
3. Place its deletion before the dependency in the reverse function.
4. If tests reveal a daemon race, document it as an RCA with root
   cause and workaround. Do not add `time.Sleep` to the write path.

**When a daemon ignores an entry:** The entry was probably written
before its dependency was processed. Check the ordering chain, not the
timing.

---

## 32. Unified Naming Convention — Consistent CONFIG_DB Key Names

Every CONFIG_DB key name that newtron derives follows a single convention:

- **ALL UPPERCASE**, single underscore (`_`) as the only separator.
- **Hyphens converted**: user-provided spec names have hyphens replaced with
  underscores and letters uppercased. `protect-re` → `PROTECT_RE`.
- **Allowed characters**: `[A-Z0-9_]` only.
- **No redundant kind in key**: the table name carries the object kind. The key
  never repeats it. `ACL_TABLE|PROTECT_RE_IN_1ED5F2C7` — not
  `ACL_TABLE|ACL_PROTECT_RE_IN_1ED5F2C7`. Just as SONiC uses `Vlan100` (not
  `VlanVlan100`), newtron keys don't echo their table.
- **Numeric IDs concatenated with type prefix**: matching SONiC convention
  (`Vlan100`, `Loopback0`), newtron uses `VNI1001`, `VLAN100`, `ETH0`, `Q0`.

This is not cosmetic. A single convention means any CONFIG_DB key can be
parsed programmatically — and any operator scanning `redis-cli` output can
read newtron-managed entries by eye.

---

## 33. Normalize at the Boundary — Spec Loader Owns Name Canonicalization

Names are normalized **once, at spec load time** — not at each point of use.
When the spec loader reads JSON files, it converts all name keys and
name-reference fields to canonical form (uppercase, hyphens → underscores)
before returning specs to callers.

After loading, every map key (`Services["TRANSIT"]`), every cross-reference
(`ServiceSpec.IngressFilter = "PROTECT_RE"`), and every name that flows into
CONFIG_DB key construction is already canonical. Operations code never calls
`NormalizeName()` — the loader already did it.

This eliminates an entire class of bugs: comparing a raw spec name against a
normalized CONFIG_DB key. It also means JSON spec files on disk are
untouched — operators write whatever case they prefer (`"protect-re"`,
`"PROTECT-RE"`, `"Protect-RE"`); the loader normalizes on read.

The pattern: validate and normalize at system boundaries (spec loading, CLI
parsing, API Save methods); trust canonical form inside the boundary.

---

## 34. Policy vs Infrastructure — Shared Objects Have Independent Lifecycles

CONFIG_DB entries fall into three categories:

| Category | Identity | Lifecycle | Examples |
|----------|----------|-----------|----------|
| **Infrastructure** | Per-interface | Created/destroyed with service apply/remove | INTERFACE IP, BGP_NEIGHBOR, VRF binding |
| **Policy** | User-named + content hash | Shared across services, independent lifecycle | ACL_TABLE, ROUTE_MAP, PREFIX_SET, COMMUNITY_SET |
| **Binding** | Per-interface | Created/destroyed with service apply/remove | ACL ports field, BGP_PEER_GROUP_AF route_map_in |

Infrastructure is 1:1 with an interface's service binding. Policy objects are
N:1 — many interfaces can reference the same ACL or route map. Bindings
connect the two.

This distinction drives the lifecycle model: infrastructure entries are
created on `ApplyService` and deleted on `RemoveService`. Policy objects are
created on first reference and deleted when the last reference is removed.
They persist across individual service removals as long as at least one
consumer remains.

The separation also enables content-hashed naming — policy objects carry a
hash of their CONFIG_DB content in their name, allowing automatic change
detection and blue-green updates without touching every consumer
simultaneously.

---

## 35. Content-Hashed Naming — Version Shared Objects by What They Write

Shared policy objects (ACLs, route maps, prefix sets, community sets) include
an 8-character content hash in their CONFIG_DB key name:

```
ACL_TABLE|PROTECT_RE_IN_1ED5F2C7
ROUTE_MAP|ALLOW_CUST_IMPORT_A1B2C3D4|10
PREFIX_SET|RFC1918_5F2A8B3E|10
```

The hash is computed from the **generated CONFIG_DB entry fields** — the actual
`map[string]string` that would be written to Redis — not the spec struct. Sorted
keys, sorted entries, SHA256, first 4 bytes as uppercase hex. This means:

- Future newtron versions that add new CONFIG_DB fields automatically produce
  different hashes (correct — new fields = new content).
- No separate "canonical form" to maintain, no version field to forget to bump.
- The hash is literally "what would this policy write to Redis?"

Dependent objects use bottom-up Merkle hashing: PREFIX_SET hashes are computed
first (leaves), then ROUTE_MAP entries reference the real PREFIX_SET names
(including their hashes), so a prefix list content change cascades through the
hash chain automatically. The cascade terminates at BGP_PEER_GROUP_AF — a
field update, not a name change.

Spec unchanged → hash unchanged → `RefreshService` is a no-op for that object.
Spec changed → new hash → new object created alongside old → interfaces migrate
one by one → old object deleted when last consumer migrates. Blue-green at the
object level, with zero disruption.

### Hash Placement: Always Suffix, Never Prefix

The content hash is always a **suffix** on the object name:
`{SERVICE}_{DIRECTION}_{HASH}`, not `{HASH}_{SERVICE}_{DIRECTION}`. This is a
deliberate coupling constraint between two independent code paths.

The forward path (`createRoutePolicy`) generates entries with hashed names. The
reverse path (`deleteRoutePoliciesConfig`) scans CONFIG_DB for entries whose key
starts with `{serviceName}_` — a prefix scan. These two code paths never call
each other; they agree on names only by naming convention.

If the hash is a suffix, the prefix scan still works:

```
ROUTE_MAP|TRANSIT_IMPORT_A1B2C3D4|10     ← starts with "TRANSIT_" ✓
PREFIX_SET|TRANSIT_IMPORT_PL_10_F3E2|10   ← starts with "TRANSIT_" ✓
```

If the hash were a prefix, the prefix scan would silently match nothing:

```
ROUTE_MAP|A1B2C3D4_TRANSIT_IMPORT|10     ← does NOT start with "TRANSIT_" ✗
```

The failure mode is particularly dangerous: the forward path (create) works
fine; only the reverse path (delete) breaks — silently leaking CONFIG_DB
entries that accumulate over time and can never be cleaned up. This breakage
would only manifest when a service is removed, which might not happen in
testing for weeks.

**Rule: content hashes are always the last component of a generated name.**
The service name prefix is the anchor that connects the forward and reverse
paths.

### Stale Hash Cleanup During RefreshService

When a spec changes and `RefreshService` runs, old-hash policy objects become
orphaned. `RemoveService` (called internally) skips shared policy deletion if
other interfaces still use the service. `ApplyService` creates new-hash objects.
The old objects are never cleaned up by normal lifecycle — `deleteRoutePoliciesConfig`
only runs when the last service user is entirely removed.

`RefreshService` solves this with a post-merge scan: after the remove+apply
cycle, it reads existing route policy objects from CONFIG_DB (Redis in online
mode, shadow in offline mode), compares against the set of objects just created
by the apply phase, and deletes the difference. This is safe because:

- All interfaces sharing a service use the same spec → the same hashes
- The shared peer group AF was already updated to reference new route map names
- Old objects are genuinely unreferenced after the peer group AF update

The binding stores `route_map_in` and `route_map_out` for self-sufficiency —
enabling future optimizations (e.g., skip the Redis scan when hashes match).

---

## 36. BGP Peer Groups — The Protocol's Native Sharing Mechanism

When multiple interfaces use the same service with BGP routing, their neighbors
share route maps, admin status, and other service-level attributes. Rather than
duplicating these on every `BGP_NEIGHBOR_AF` entry, newtron creates a
**BGP_PEER_GROUP** named after the service. Neighbors reference the peer group;
shared attributes live on `BGP_PEER_GROUP_AF`.

```
BGP_PEER_GROUP|TRANSIT                  → { admin_status: up }
BGP_PEER_GROUP_AF|TRANSIT|ipv4_unicast  → { route_map_in: ..., route_map_out: ... }
BGP_NEIGHBOR|default|10.1.0.1           → { peer_group: TRANSIT, asn: 65002 }
```

This is BGP's own mechanism for template inheritance — not an invention. When a
route map hash changes, one update to the peer group AF propagates to all
neighbors. Without peer groups, every neighbor AF entry would need individual
updates.

Peer groups are created on first `ApplyService` for a service with BGP routing,
and deleted when the last interface using that service is removed. Topology-level
underlay peers (spine-leaf links) do NOT use peer groups — each has unique
attributes.

---

## 37. Verification Must Not Pass Vacuously

A verification check that finds zero items to verify must **fail**, not pass.
An empty result set means the precondition isn't met — the daemon hasn't
processed entries yet, the table hasn't been populated, or the query returned
no data. It does not mean "all checks passed."

### The Vacuous Truth Trap

```go
// WRONG — passes when results is empty
for _, hc := range results {
    if hc.Status != "pass" {
        return false  // keep polling
    }
}
return true  // "all pass" — but zero items were checked

// CORRECT — empty results means precondition not met
if len(results) == 0 {
    return false  // keep polling
}
for _, hc := range results {
    if hc.Status != "pass" {
        return false
    }
}
return true
```

This class of bug is insidious because:

- **It passes in testing.** The poll returns "success" instantly because the
  precondition (e.g., frrcfgd creating BGP sessions) hasn't happened yet.
  Subsequent checks may or may not catch the missing state, depending on
  timing.
- **It masks real failures.** If a daemon silently drops entries, the
  verification sees zero results and reports success.
- **It creates timing-dependent flakiness.** Sometimes the daemon processes
  entries before the first poll (real pass); sometimes it doesn't (vacuous
  pass followed by a point-in-time check that fails).

### Observation Lag

Even when a polling check correctly detects state, a subsequent point-in-time
observation may contradict it. This happens because the observation tool (e.g.,
`vtysh -c 'show bgp summary json'`) has its own internal latency — FRR's
routing daemon knows about the peer, but the JSON serializer hasn't caught up.

The pattern: a polling check confirms BGP sessions are Established, then a
health check run 1 second later reports "BGP neighbor not found in FRR." The
sessions didn't disappear — the observation tool lagged behind the daemon's
internal state.

**Rule: when a polling check passes but a subsequent point-in-time observation
contradicts it, the observation is stale — not the poll.** Add a brief settle
delay between the poll and the observation to let the observation tool catch
up. This is not a `time.Sleep` in the write path (prohibited by §31); it is a
read-side settling delay in the test suite.

### Application

Every polling-based verification function must:

1. **Check for empty results** and return false (keep polling).
2. **Document the minimum expected count** if known — e.g., "at least N BGP
   neighbors should exist based on CONFIG_DB."
3. **Distinguish between "precondition not met" and "check failed"** in log
   messages, so flaky tests can be diagnosed from output alone.

---

## 38. Convergence Budget — Every Entry Costs Time

Each CONFIG_DB entry newtron writes extends the post-provisioning convergence
window. SONiC daemons process entries sequentially from keyspace notifications;
adding N entries to a composite adds O(N) processing time to the convergence
window. This is not a bug — it is the fundamental cost model of SONiC's
event-driven architecture.

### The Cost Model

After `DeliverComposite` writes a full device config via `config reload`,
every subscribed daemon receives a burst of notifications and processes them
one by one. The total convergence time is approximately:

```
T_converge ≈ Σ (entries_per_daemon × processing_time_per_entry)
```

Processing time varies by daemon and platform (see §31's latency table), but
the key insight is that it's **linear in entry count**. When a design change
adds entries — even small, seemingly harmless ones — the convergence window
grows.

### Real Example

Adding BGP_PEER_GROUP support (§36) added 2 entries per service
(BGP_PEER_GROUP + BGP_PEER_GROUP_AF). In the 2node-ngdp-service topology with one
service on each switch, this added 4 entries total. frrcfgd now processes
these entries before creating BGP neighbors in FRR — adding approximately
1-2 seconds to the BGP convergence window.

The verify-health test had a timing margin of ~1 second. The 2 extra entries
consumed that margin. The test went from "usually passes" to "usually fails"
— not because anything was broken, but because the convergence budget was
exceeded.

### Implications for Design

1. **Shared objects amortize their cost.** A BGP_PEER_GROUP shared across 10
   neighbors costs 2 entries (the group + AF). Without sharing, those 10
   neighbors would each carry route-map fields on their BGP_NEIGHBOR_AF — the
   same entry count but worse update semantics. Sharing reduces update cost
   (one AF update vs. ten), even if it adds initial creation cost.

2. **Test timeouts must be proportional to entry count.** A topology with 50
   CONFIG_DB entries converges faster than one with 200. Fixed timeouts
   (e.g., "wait 30 seconds after provision") will be either too short for
   large configs or wastefully long for small ones. Use polling with generous
   upper bounds instead.

3. **Count entries when adding features.** Before adding a new table or entry
   type, estimate how many entries it adds per service × per interface × per
   device. Multiply by the daemon's per-entry latency from §31's table. If the
   total exceeds the test suite's timing margin, increase the margin
   preemptively — don't discover it from flaky tests.

4. **Prefer field updates over new entries.** Updating a field on an existing
   entry (e.g., adding `route_map_in` to BGP_PEER_GROUP_AF) generates one
   notification. Creating a new entry generates one notification plus any
   daemon-internal initialization. When the choice exists, field updates are
   cheaper.

---

## 39. Definition Is Network-Scoped; Execution Is Device-Scoped

The network is the unit of definition — specs, services, policies, prefix
lists exist at the network level and have their own lifecycle (create,
modify, delete) independent of any device. The device is the unit of
execution — apply, remove, verify, observe operate on a specific device's
CONFIG_DB reality.

These two lifecycles must not be coupled. A service can be defined before
any device is connected. A device can consume a service defined after it
connected. Neither layer should require the other to be in a particular
state for its own operations to succeed.

### Why this matters

§9 (Hierarchical Spec Resolution) describes how specs are merged at node
creation time: network → zone → profile, lower level wins. This merge
produces a `ResolvedSpecs` snapshot — a per-device flattened view for fast
lookup. The snapshot is correct at creation time, but it becomes a closed
world: specs added to the network after the snapshot was built are invisible.

This is not hypothetical. The spec authoring API (`SaveService`,
`SavePrefixList`, `SaveRoutePolicy`) adds entries to the network-level maps
at runtime. If `ResolvedSpecs` only reads its snapshot, a newly created
service is invisible to every connected device until the server restarts.

### The fix: snapshot with live fallback

`ResolvedSpecs.Get*` methods check the merged snapshot first (preserving
override semantics — profile wins over zone wins over network). On miss,
they fall through to the network-level maps via `network.Get*`. This keeps
the hierarchy intact for overrides while making the network level open for
additions:

```
ResolvedSpecs.GetService("TRANSIT")
  1. Check merged snapshot → found (profile override) → return it
  2. Miss → fall through to network.GetService("TRANSIT") → found → return it
  3. Miss at both levels → "service not found" error
```

The alternative — rebuilding all snapshots on every write — is correct but
expensive and couples the write path to the set of connected nodes. The
fallback-on-miss approach is O(1) per lookup and requires no coordination.

### Implications

1. **Per-device resolution snapshots must fall through to live network maps
   on miss.** Any new `Get*` method on `ResolvedSpecs` must include the
   network fallback. A merged-map-only lookup is a bug.

2. **Orchestrators model the two scopes as distinct step types.** In newtrun,
   network-level steps (create-prefix-list, create-route-policy, create-service)
   call `r.Client.*` directly with no `devices:` field. Device-level steps
   (apply-service, remove-service) use `executeForDevices`. When adding new
   newtrun actions, pick the pattern based on whether the operation targets
   the spec layer or a device.

3. **The spec authoring API never touches a device. The device operation API
   never creates specs.** The boundary is the `Get*` interface on
   `SpecProvider` — devices read definitions, they don't own them.

---

## 40. Greenfield — Backwards Compatibility Is a Non-Goal

newtron is a greenfield system. It does not inherit users, deployments, or API
contracts from a predecessor. There is no installed base to protect, no migration
path to maintain, no deprecated interface to keep working.

This has concrete consequences for how code is written:

### No compatibility shims

When a data format, key schema, or API shape changes, change it everywhere in
one commit. Do not:

- Rename unused variables to `_oldName` to preserve symbols
- Re-export moved types from their old location
- Add `// removed: X` comments as tombstones
- Accept both old and new formats with a detection heuristic
- Add feature flags or version checks to route between old and new paths

If something is unused, delete it. If something moved, update all references.
If a format changed, change all producers and consumers. One commit, fully clean.

### No legacy format handling in the runtime path

Factory images, community defaults, and third-party tools may leave artifacts
in CONFIG_DB (legacy bgpcfgd entries, stale DEVICE_METADATA fields, default
config_db.json entries). These are **not newtron's problem at runtime**.

The correct approach:

1. **Initialization cleans up** — `newtron init` is the one-time boundary where
   factory state is scrubbed and the device is prepared for newtron management.
   Cleanup code belongs here, not scattered through operations.
2. **Operations assume a clean device** — after init, every operation assumes the
   device has only newtron-managed and community-standard entries. Operations do
   not check for or work around legacy formats.
3. **Detection functions check newtron's own schema** — `BGPConfigured()` checks
   `BGP_GLOBALS["default"]` (the frrcfgd entry newtron creates), not legacy
   indicators like `DEVICE_METADATA.bgp_asn` or bare-IP `BGP_NEIGHBOR` keys.

### No API versioning

The public API (`pkg/newtron/`) has exactly one version: current. When a type,
method signature, or behavior changes, all consumers (CLI, newtrun, HTTP server)
are updated in the same commit. There is no `v1`/`v2` namespace, no deprecation
period, no `Option` structs with backwards-compatible zero values.

### Why this matters

Compatibility code is the primary source of accidental complexity in mature
systems. Every `if oldFormat { ... } else { ... }` branch doubles the test
surface, doubles the bug surface, and confuses readers who must understand both
paths to reason about behavior. In a greenfield system, this complexity is
entirely self-inflicted — there is no external force requiring it.

The rule is simple: **write code for the system as it is today, not as it was
yesterday.**

### Exception: SONiC release differences

This principle applies to newtron's own code — its types, APIs, key schemas, and
internal formats. It does **not** apply to the SONiC platform underneath.

SONiC releases change CONFIG_DB schemas, daemon behavior, YANG models, and
default configurations between releases (e.g., 202411 → 202505). newtron must
support multiple SONiC releases because operators run different versions across
their fleet. This is not backwards compatibility — it is multi-platform support,
analogous to a compiler targeting multiple CPU architectures.

When SONiC releases diverge, the correct approach is platform-aware code paths
gated on the detected SONiC version, not version-unaware code that tries to
work everywhere by accident. The version detection and branching should be
explicit and centralized (in the device layer), not scattered across operations.

---

## 41. Multi-Version Readiness — Protect the Seams

newtron will eventually manage devices running different SONiC releases
simultaneously (202411, 202505, future). The architecture must support this
without scattering version checks across the codebase. This is not a feature
to build now — it is a set of **architectural boundaries to preserve** so that
multi-version support can be added later by layering data-driven overrides onto
the existing structure.

### The three boundaries that make multi-version possible

**1. All CONFIG_DB interaction is funneled through Device.**

The `device/sonic/` package is the sole point of contact with Redis. No
operation in `node/` or above constructs raw Redis commands or knows Redis
database numbers. This means version-aware schema resolution, key format
differences, and parser variations can be introduced in one package without
touching operations code.

Preserving principles: §1 (SONiC is a database), §11 (single-owner tables),
§13 (import direction).

**2. All device-scoped operations flow through Node.**

Every operation goes `CLI → API → Node → Device → Redis`. Node holds the
Device, which will hold the detected version. Any operation that needs
version-aware behavior has access to the version through this chain without
new plumbing. There is no alternative path that bypasses Node.

Preserving principles: §26 (Node as device isolation boundary), §24 (respect
abstraction boundaries).

**3. Config functions are pure — parameters in, entries out.**

The `*_ops.go` config generators take domain parameters and return
`[]sonic.Entry`. They do not reach into Redis, Device, or global state.
Adding a version parameter (or a version-aware schema lookup) to their
inputs lets them produce different entries for different releases without
changing their structure.

Preserving principles: §21 (pure config functions), §12 (file-level cohesion).

### What this means for day-to-day development

These boundaries exist today for other good reasons. This principle says:
**do not erode them**, because they are load-bearing for multi-version support.

Concretely, do not:

- Add direct Redis calls outside `device/sonic/` — even "just one quick read"
- Add CONFIG_DB key construction outside the owning `*_ops.go` file
- Bypass Node to call Device methods from operations code
- Hardcode SONiC-version-specific behavior in operations without a version
  parameter (use platform feature checks or defer to the device layer)
- Put schema knowledge (field names, value constraints, key formats) in the
  transport or API layer

### When multi-version is implemented

The expected shape — not a design, just the architectural direction:

- **Version detection**: `Device.Connect()` reads the SONiC version, stores it
  on Device. Available to all downstream code through the existing call chain.
- **Schema overrides**: base schema (current `schema.go`) + version-specific
  deltas. Validation resolves the effective schema for the device's version.
  Data-driven, not code-branched.
- **Feature/behavior overrides**: extend `PlatformSpec` with version constraints
  alongside feature flags. Operations consult capabilities, not version numbers.
- **Config function overrides**: where a table's key format or field set changes
  between releases, the config function receives the version context and
  produces the correct entries. The override is in the config function (which
  owns the table), not in the caller.

The guiding rule: **version differences are data (schema deltas, capability
tables), not code (if/else branches scattered across operations)**. This is
the same principle as §40's greenfield rule applied to the platform layer —
avoid compatibility code, use declarative overrides.

---

## 42. The Opinion Is in the Pattern

Every piece of SONiC configuration — a VLAN, a BGP session, a service
binding, an ACL rule — can be configured many ways in CONFIG_DB. newtron
offers one pattern for each. The pattern is the opinion.

This is what "opinionated configuration primitives" means. The opinions
live at the smallest possible level — the individual CONFIG_DB entry
pattern — not at the network level. newtron does not prescribe a network
topology. It prescribes how each unit of configuration should look. The
operator composes those units into whatever topology they need — any
underlay, any overlay, any scale. The configuration architecture is
embedded in the primitives. The topology architecture is the operator's
choice.

This produces two distinct layers:

- **Configuration architecture** — one pattern per primitive. How a VLAN
  is structured, how a BGP neighbor is established, how a service binds
  to an interface. This is where newtron's opinions live. These patterns
  can evolve — all-eBGP today, new routing models tomorrow — but at any
  point in time, each primitive has exactly one pattern.

- **Topology architecture** — the operator's composition. Spine-leaf,
  hub-spoke, single overlay, multiple overlays, two nodes or two hundred.
  newtron constrains the building blocks, not the building.

Consistent primitives compose into a coherent network. This is not a
hope — it is a structural consequence of every building block following
the same patterns.

---

## 43. Delivery Over Generation

Generating configuration from patterns is familiar territory —
configlet templates work similarly. The hard problem is what happens
after generation: delivering each primitive to the device safely and
maintaining the integrity of device state as operations accumulate over
time.

Without delivery guarantees, configuration degrades. Partial applies
leave orphaned entries that nothing knows how to clean up. Overlapping
writes from different operations munge shared state. Teardown, forced
to guess what belongs to whom by inspecting current device state, corrupts
what it doesn't understand.

newtron's delivery pipeline prevents all three:

1. **Validated against schema.** Every CONFIG_DB entry passes YANG-derived
   validation before reaching the device — invalid values never land.
   (§30)
2. **Applied atomically.** Every mutating operation produces a ChangeSet
   that is computed fully before any write occurs — partial state never
   accumulates. Dry-run is the default; execution is opt-in. (§14, §17)
3. **Verified by re-reading.** After execution, newtron re-reads every
   entry it wrote and diffs against the ChangeSet — silent failures
   don't go unnoticed. (§5, §17)
4. **Reversible by construction.** Every forward operation records what it
   did — on the device, as a service binding. Teardown reconstructs from
   what was actually done, not from device state that other operations
   may have changed since. (§23)

These guarantees are properties of the ChangeSet pipeline, not of any
specific primitive. When a new primitive is added, it inherits them
automatically. When an existing primitive changes, they remain. The
primitives are the variable. The delivery contract is the invariant.

---

## 44. Faithful Enforcement — The Primitives Change, the Contract Doesn't

The specific primitives can change — all-eBGP today, ISIS tomorrow;
service-on-interface today, new binding models later. What doesn't
change is the enforcement contract: whatever primitives newtron supports,
it enforces them faithfully.

This is the synthesis of §42 and §43. The opinions (§42) define what
each primitive looks like. The delivery pipeline (§43) ensures each
primitive arrives safely. Together they form the enforcement contract:
newtron will deliver your primitives to the device correctly, and it
will be able to undo them cleanly.

The enforcement machinery — schema validation, atomic application,
post-write verification, symmetric reversal — is not a feature list.
It is how the primitives maintain their own integrity.

---

## 45. Summary

| Principle | One-Line Rule |
|-----------|---------------|
| SONiC is a database | Interact through Redis, not CLI parsing; CLI is an exception, not the norm |
| Platform patching | Patch what's broken; don't build parallel infrastructure around it |
| One level of abstraction per program | newtlab realizes the topology, newtron defines opinionated configuration primitives and delivers them safely, orchestrators decide what gets applied where |
| Objects own their methods | The interface is the point of service; a method belongs to the smallest object that has all the context to execute it |
| If you change it, you verify it | The tool that writes the state must be able to verify the write |
| Prevent bad writes | Application-level referential integrity for a database that has none; refuse invalid state, don't detect damage |
| Spec vs config | Intent is declarative and version-controlled; state is imperative and generated |
| The device is source of reality | The device is the reality (not truth — reality may be wrong); specs are the intent; operations transform reality using intent; basic ops check preconditions, service ops trust the binding record |
| Hierarchical spec resolution | Define once at the broadest scope; override only where necessary; resolve once at node creation |
| DRY across programs | Every capability exists in exactly one place, even across program boundaries |
| Single-owner CONFIG_DB tables | One file owns each table; everyone else calls the owner |
| File-level feature cohesion | Understand a feature by reading one file; change a table format by changing one file |
| Import direction | Dependencies flow from orchestration to primitives, never backward |
| Dry-run as first-class | Default to preview; execution is opt-in; this forces clean separation of compute and apply |
| Files, not APIs | Programs communicate through the spec directory, not through shared libraries or services |
| Observation vs assertion | Assert your own work; observe everything else as structured data |
| The ChangeSet is universal | One contract for all mutations; one verification method for all operations |
| Episodic caching | Refresh once at episode start; read many times within; never carry cache across episode boundaries. SSH connections are reused; CONFIG_DB snapshots are not |
| CLI-first research | Never assume a CONFIG_DB path works without first verifying it via CLI on a real device |
| Documentation freshness | Grep finds what you already know is wrong; audits find what you don't know is wrong |
| Pure config functions | Generate entries in pure functions; orchestrate them in operations |
| On-demand Interface state | Read state from the snapshot, not from cached fields; snapshots are refreshed per episode |
| Symmetric operations | If newtron creates it, newtron must be able to remove it; every forward config generator must have a reverse |
| Never enter unrecoverable states | If the safety net (intent record) cannot be written, do not create a situation that needs one |
| Structural proof over heuristics | Use structural facts (lock acquired + intent exists = previous holder crashed) instead of heuristic thresholds (staleness timers) |
| Respect abstraction boundaries | If an object knows its own identity, callers must not re-supply it |
| Verb-first, domain-intent naming | Verbs come first (`createVlan` not `vlanCreate`); names describe domain intent, not CONFIG_DB mechanics |
| Node as device isolation boundary | Every device-scoped operation is a Node method; cross-device coordination belongs in the orchestrator |
| Abstract Node | Same code path, different initialization; the shadow enforces the same ordering constraints as a physical device |
| Public API boundary | Public types expose caller intent; internal types expose implementation; orchestrators are consumers, not insiders; a boundary justified by one type applies uniformly to all types at that boundary |
| Transparent transport | The transport layer is a mechanical shim; domain logic lives in the public API; new endpoints are additive with zero infrastructure changes |
| YANG-derived schema validation | YANG is the authority for value constraints; fail closed on unknown tables and fields; validate every ChangeSet before any Redis write |
| Write ordering and daemon settling | Config functions encode dependency order in their return slice; daemon settling is verified by polling, never by sleeping in the write path |
| Unified naming convention | ALL UPPERCASE, single underscore, no redundant kind in key; `[A-Z0-9_]` only; any CONFIG_DB key can be parsed programmatically |
| Normalize at the boundary | Spec loader normalizes all name keys and references at load time; operations code never calls `NormalizeName()` |
| Policy vs infrastructure | Infrastructure is 1:1 with interface; policy objects are N:1, shared, created on first reference, deleted on last |
| Content-hashed naming | Shared policy objects carry an 8-char hash of their CONFIG_DB content; hash is always a suffix (never prefix) to preserve prefix-scan deletion; spec unchanged → hash unchanged → no-op; RefreshService scans for stale hashes and cleans up orphaned objects |
| BGP peer groups | Service-named peer groups are the protocol's native sharing mechanism; shared attributes live on `BGP_PEER_GROUP_AF`, not duplicated per neighbor |
| Verification must not pass vacuously | Zero items to verify = precondition not met = keep polling; observation tools lag behind daemon state; settle before point-in-time checks |
| Convergence budget | Every CONFIG_DB entry costs convergence time; count entries when adding features; test timeouts must be proportional to entry count |
| Definition is network-scoped; execution is device-scoped | Specs live at the network level with their own lifecycle; devices consume them via `Get*` with live fallback; the two lifecycles must not be coupled |
| Greenfield | No backwards compatibility; no legacy format handling in runtime; no API versioning; write code for the system as it is today, not as it was yesterday |
| Multi-version readiness | All Redis through Device, all operations through Node, all entry generation in pure config functions — preserve these seams so version overrides are data-driven additions, not scattered code branches |
| The opinion is in the pattern | One pattern per CONFIG_DB primitive; configuration architecture lives in the primitives, topology architecture is the operator's choice |
| Delivery over generation | Generating config is table stakes; the delivery pipeline (validate, atomic, verify, record) prevents partial applies, munged state, and blind teardown |
| Faithful enforcement | The primitives change, the contract doesn't; the enforcement machinery is how the primitives maintain their own integrity |
| Faithful enforcement | The primitives change, the contract doesn't — validated against schema, applied atomically, verified by re-reading, reversible by construction; these are properties of the pipeline, not of any specific primitive |
