# Design Principles

This document describes the architectural principles behind newtron,
newtlab, and newtest — and the philosophy that keeps them coherent as a
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
newtlab (deploy VMs), newtest (E2E testing) — not because three is a
nice number, but because each program has a fundamentally different
relationship with the world:

- **newtlab** realizes a topology. It reads newtron's `topology.json` and
  brings it to life — deploying QEMU VMs (primarily SONiC) and wiring
  them together using socket-based links across one or more servers. No
  root, no bridges, no Docker. It doesn't define the topology or touch
  device configuration — it makes the topology physically exist.
- **newtron** is an opinionated automation for SONiC devices. It enforces
  a network design intent — the spec files define the constraints of
  what the network should look like — while allowing many degrees of
  freedom within those constraints for actual deployments. It operates
  on a single device at a time, translating specs into CONFIG_DB through
  an SSH tunnel. It never talks to two devices at once.
- **newtest** is an orchestrator specifically for E2E testing. It tests
  two things: that newtron's automation produces correct device state,
  and that SONiC software on each device behaves correctly in its role
  (spine, leaf, etc.). It deploys topologies (via newtlab), provisions
  devices (via newtron), then asserts correctness — both per-device
  and across the fabric.

newtron and newtlab are general-purpose tools. newtest is not — it exists
to test newtron and the SONiC stack. Other orchestrators could be built on top of
newtron and newtlab for different purposes (production deployment,
CI/CD pipelines, compliance auditing), and the architecture is designed
to support that. newtron's observation primitives (`GetRoute`,
`RunHealthChecks`) return structured data precisely so that *any*
orchestrator can consume them — not just newtest.

These boundaries follow from a single rule: **each program owns exactly
one level of abstraction**. newtlab owns VM realization — turning a
topology spec into running, connected VMs. newtron owns single-device
configuration — translating specs into CONFIG_DB entries. Orchestrators
own the "what, where, and in what order" — which devices to provision,
which services to apply to which interfaces, with what parameters, and
in what sequence. newtest is the first orchestrator, focused on E2E
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

An `Interface` knows its parent `Device`, which knows its parent
`Network`. When you call `Interface.ApplyService()`, the interface
reaches up to the Device for the AS number, up to the Network for the
service spec, and combines them with its own identity to produce
CONFIG_DB entries. No external function orchestrates this — the object
has everything it needs through its parent chain.

```
Network
  ├── owns: specs (services, filters, VPNs, sites, platforms)
  ├── methods: GetService(), GetFilter(), GetZone()
  │
  └── Device (parent: Network)
        ├── owns: profile, resolved config, Redis connections
        ├── methods: ASNumber(), BGPNeighbors(), VerifyChangeSet()
        │
        └── Interface (parent: Device)
              ├── owns: interface state, service bindings
              └── methods: ApplyService(), RemoveService(), RefreshService()
```

Interface delegates to Device for infrastructure (Redis connections,
CONFIG_DB cache, specs) just as a VLAN interface on a real switch
delegates to the forwarding ASIC for packet processing. The delegation
does not make Interface a "forwarding layer" — it makes Interface a
logical point of attachment that the underlying infrastructure services.

This means:

- **ApplyService lives on Interface**, not on Device or Network, because
  the interface is the entity being configured — the point where a
  service becomes real. The interface's identity (name, IP, parent
  device) is part of the translation context.

- **VerifyChangeSet lives on Device**, not on Network or in a utility
  package, because the device holds the Redis connection needed to re-read
  CONFIG_DB.

- **GetService lives on Network**, not on Device, because services are
  network-wide definitions that exist independent of any device.

The CLI mirrors this: `newtron -n network -d device -i interface verb`.
You select an object, then invoke a method on it. The flags are not
configuration — they are object selection.

The general principle: **a method belongs to the smallest object that has
all the context to execute it**. If an operation needs the interface name,
the device profile, and the network specs, it lives on Interface (which
can reach all three through its parent chain). If it only needs the Redis
connection, it lives on Device.

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
newtron's verification**. When newtest needs to check CONFIG_DB on a
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
reads all specs to provision devices. newtest reads scenario definitions
that reference topology specs. No program imports another's packages or
calls another's API. They communicate through files.

Config is generated by newtron and not intended to be hand-edited. But
if external changes are made to CONFIG_DB — by an admin, another tool,
or a SONiC daemon — newtron treats the device state as authoritative.
Incremental operations read and respect what's on the device, not what
the spec says should be there. See Principle 8.

---

## 8. The Network Is Source of Truth

The device CONFIG_DB is ground reality. Spec files are templates and
intent, but once configuration is applied, the device state is what
matters. If an admin edits CONFIG_DB directly — via the SONiC CLI,
Redis, or another tool — that edit is the new reality. newtron does not
fight it or attempt to reconcile back to the spec.

This shapes every incremental operation in the system. **Provisioning
(CompositeOverwrite)** is the one exception where intent replaces
reality — it delivers a full composite config and overwrites whatever
was there. Every other operation is `Device + Delta → Device`: it reads
what's on the device, applies a change, and leaves the result on the
device. The spec provides the intent; the device provides the context.

**NEWTRON_SERVICE_BINDING records live on the device**, not in spec
files. When a service is applied to an interface, newtron writes a
binding record to CONFIG_DB that captures exactly what was applied —
which VLANs, VRFs, ACLs, and VNIs were created for that service. When
the service is later removed, `RemoveService` reads this binding from
the device to know what to undo. It does not re-derive the removal from
the spec because the spec may have changed, and what matters is what was
actually applied.

**Idempotency filtering** also operates on device reality, not spec
intent. When applying a service, newtron checks whether VLANs and VRFs
already exist on the device — because they may have been created by a
different service or an external tool. It respects what's already there
rather than blindly re-creating it. This check happens against
CONFIG_DB, not against a spec-derived expected state.

This is why newtron is not a Terraform or Kubernetes desired-state
reconciler. A reconciler needs a single canonical source of truth to diff
the device against. For incremental operations, no such canonical source
exists — the "desired state" of the device is its current state plus the
requested change, and the current state can only be read from the device
itself. Building a reconciler would require newtron to maintain a shadow
copy of the full device config, keep it in sync across all external
changes, and resolve conflicts — that is a fundamentally different system
with fundamentally different guarantees.

**The device is the truth; specs are the intent; operations transform
truth using intent.**

---

## 9. Hierarchical Spec Resolution — Network, Zone, Node

Specs are organized in a three-level hierarchy: network → zone → node
(device profile). Each level can define the same eight overridable spec
maps: Services, Filters, IPVPNs, MACVPNs, QoSPolicies, QoSProfiles,
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

**One spec directory.** newtlab, newtron, and newtest all read from the
same `specs/` directory. `topology.json` belongs to newtlab and newtest —
it defines the physical topology for VM deployment and test orchestration.
newtron does not require it. newtlab reads the `devices` and `links`
sections to deploy VMs. newtest reads the topology to understand device
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
table. Composite operations (ApplyService, SetupEVPN, ApplyBaseline,
topology provisioning) call the owning primitives and merge their
ChangeSets rather than constructing entries inline.

The ownership map:

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
a service that newtest connects to. The programs are not microservices.

Instead, they communicate through the spec directory:

- newtlab writes `ssh_port`, `console_port`, and `mgmt_ip` into profile
  files after deploying VMs.
- newtron reads those profile files and uses the ports to connect.
- Orchestrators (like newtest) invoke newtlab and newtron as CLI commands,
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

5. **Targeted test first.** Create a targeted newtest suite that tests
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

```go
// Pure config function — owned by vlan_ops.go
func vlanConfig(vlanID int, members []string) []sonic.Entry

// Pure delete config function — scans ConfigDB for related entries
func vlanDeleteConfig(configDB *sonic.ConfigDB, vlanID int) []sonic.Entry
```

Operations call these functions and wrap the result:

```go
// Simple CRUD — uses the op() helper
func (n *Node) CreateVLAN(ctx context.Context, vlanID int, ...) (*ChangeSet, error) {
    return n.op("create-vlan", vlanName, ChangeAdd,
        func(pc *PreconditionChecker) { pc.RequireVLANNotExists(vlanID) },
        func() []sonic.Entry { return vlanConfig(vlanID, members) },
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
| `BindACL` | `UnbindACLFromInterface` |

When adding a new operation that creates CONFIG_DB state, the
corresponding removal operation is not optional — it is part of the
feature. Ship both or ship neither.

**If newtron creates it, newtron must be able to remove it. No orphans,
no manual cleanup, no `redis-cli` required.**

---

## 24. Summary

| Principle | One-Line Rule |
|-----------|---------------|
| SONiC is a database | Interact through Redis, not CLI parsing; CLI is an exception, not the norm |
| Platform patching | Patch what's broken; don't build parallel infrastructure around it |
| One level of abstraction per program | newtlab realizes the topology, newtron translates specs to config, orchestrators decide what gets applied where |
| Objects own their methods | The interface is the point of service; a method belongs to the smallest object that has all the context to execute it |
| If you change it, you verify it | The tool that writes the state must be able to verify the write |
| Prevent bad writes | Application-level referential integrity for a database that has none; refuse invalid state, don't detect damage |
| Spec vs config | Intent is declarative and version-controlled; state is imperative and generated |
| The network is source of truth | The device is the truth; specs are the intent; operations transform truth using intent |
| Hierarchical spec resolution | Define once at the broadest scope; override only where necessary; resolve once at node creation |
| DRY across programs | Every capability exists in exactly one place, even across program boundaries |
| Single-owner CONFIG_DB tables | One file owns each table; everyone else calls the owner |
| File-level feature cohesion | Understand a feature by reading one file; change a table format by changing one file |
| Import direction | Dependencies flow from orchestration to primitives, never backward |
| Dry-run as first-class | Default to preview; execution is opt-in; this forces clean separation of compute and apply |
| Files, not APIs | Programs communicate through the spec directory, not through shared libraries or services |
| Observation vs assertion | Assert your own work; observe everything else as structured data |
| The ChangeSet is universal | One contract for all mutations; one verification method for all operations |
| Episodic caching | Refresh once at episode start; read many times within; never carry cache across episode boundaries |
| CLI-first research | Never assume a CONFIG_DB path works without first verifying it via CLI on a real device |
| Documentation freshness | Grep finds what you already know is wrong; audits find what you don't know is wrong |
| Pure config functions | Generate entries in pure functions; orchestrate them in operations |
| On-demand Interface state | Read state from the snapshot, not from cached fields; snapshots are refreshed per episode |
| Symmetric operations | If newtron creates it, newtron must be able to remove it; no orphans, no manual cleanup |
