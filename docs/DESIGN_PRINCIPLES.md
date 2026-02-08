# Design Principles

This document describes the architectural principles behind newtron,
vmlab, and newtest — and the philosophy that keeps them coherent as a
system. It is intended to be read before the HLDs and LLDs, as a guide
for understanding *why* things are the way they are.

---

## 1. Two Tools and an Orchestrator

The system is split into three programs — newtron (provision devices),
vmlab (deploy VMs), newtest (E2E testing) — not because three is a
nice number, but because each program has a fundamentally different
relationship with the world:

- **vmlab** realizes a topology. It reads newtron's `topology.json` and
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
  (spine, leaf, etc.). It deploys topologies (via vmlab), provisions
  devices (via newtron), then asserts correctness — both per-device
  and across the fabric.

newtron and vmlab are general-purpose tools. newtest is not — it exists
to test newtron and the SONiC stack. Other orchestrators could be built on top of
newtron and vmlab for different purposes (production deployment,
CI/CD pipelines, compliance auditing), and the architecture is designed
to support that. newtron's observation primitives (`GetRoute`,
`RunHealthChecks`) return structured data precisely so that *any*
orchestrator can consume them — not just newtest.

These boundaries follow from a single rule: **each program owns exactly
one level of abstraction**. vmlab owns VM realization — turning a
topology spec into running, connected VMs. newtron owns single-device
configuration — translating specs into CONFIG_DB entries. Orchestrators
own the "what, where, and in what order" — which devices to provision,
which services to apply to which interfaces, with what parameters, and
in what sequence. newtest is the first orchestrator, focused on E2E
testing.

If you're unsure where something belongs, ask: "does this decide what
gets applied where, or how something gets applied?" The former belongs
in an orchestrator. The latter belongs in newtron. "Does this require
knowing about device configuration at all?" If no, it belongs in vmlab.

---

## 2. Objects Own Their Methods

newtron uses an object-oriented architecture where methods belong to the
object that has the context to execute them. This is the most important
structural decision in the system.

An `Interface` knows its parent `Device`, which knows its parent
`Network`. When you call `Interface.ApplyService()`, the interface
reaches up to the Device for the AS number, up to the Network for the
service spec, and combines them with its own identity to produce
CONFIG_DB entries. No external function orchestrates this — the object
has everything it needs through its parent chain.

```
Network
  ├── owns: specs (services, filters, VPNs, sites, platforms)
  ├── methods: GetService(), GetFilterSpec(), GetRegion()
  │
  └── Device (parent: Network)
        ├── owns: profile, resolved config, Redis connections
        ├── methods: ASNumber(), BGPNeighbors(), VerifyChangeSet()
        │
        └── Interface (parent: Device)
              ├── owns: interface state, service bindings
              └── methods: ApplyService(), RemoveService(), RefreshService()
```

This means:

- **ApplyService lives on Interface**, not on Device or Network, because
  the interface is the entity being configured. The interface's identity
  (name, IP, parent device) is part of the translation context.

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

## 3. If You Change It, You Verify It

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

## 4. Spec vs Config — Intent vs State

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
programs. vmlab reads topology and platform specs to deploy VMs. newtron
reads all specs to provision devices. newtest reads scenario definitions
that reference topology specs. No program imports another's packages or
calls another's API. They communicate through files.

---

## 5. Don't Repeat Yourself — Across Programs

DRY applies not just within a single codebase, but across the entire
system. Every capability exists in exactly one place:

**One spec directory.** vmlab, newtron, and newtest all read from the
same `specs/` directory. `topology.json` belongs to newtron — it defines
the network's devices, interfaces, and links. vmlab reads the same file
because it needs to realize that topology as running VMs for testing.
The `vmlab` section in `topology.json` is vmlab borrowing space in
newtron's file, not the other way around. vmlab reads the `devices`
and `links` sections. newtron reads the `devices` and `interfaces`
sections. Neither maintains its own copy.

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
once. vmlab reads VM-specific fields (image, memory, NIC driver).
newtron reads hardware fields (HWSKU, port count). Orchestrators read
capability fields (dataplane support). Each consumer takes what it needs
from the same definition.

**One profile per device.** A device profile starts with operator-authored
fields (loopback IP, site, platform). vmlab adds runtime fields (SSH port,
console port, management IP). newtron reads the combined profile. There
is no separate vmlab state file that newtron must also consult — vmlab
writes its output *into the same profile* newtron already reads.

The anti-pattern this prevents: an orchestrator implementing its own
CONFIG_DB reader "because it needs a slightly different format." Or
vmlab maintaining its own device inventory "because it needs extra
fields." Every time a capability is duplicated, the copies drift. The
system prevents drift by having one authoritative implementation of
each capability.

---

## 6. Dry-Run as a First-Class Mode

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

## 7. Programs Communicate Through Files, Not APIs

vmlab does not expose an API that newtron calls. newtron does not expose
a service that newtest connects to. The programs are not microservices.

Instead, they communicate through the spec directory:

- vmlab writes `ssh_port`, `console_port`, and `mgmt_ip` into profile
  files after deploying VMs.
- newtron reads those profile files and uses the ports to connect.
- Orchestrators (like newtest) invoke vmlab and newtron as CLI commands,
  passing the spec directory path.

This means:

- **No shared libraries.** A change to newtron's internal types does not
  require rebuilding vmlab.
- **No runtime coordination.** vmlab exits after deploying. newtron exits
  after provisioning. They don't need to be alive at the same time.
- **No service discovery.** newtron doesn't ask "where is vmlab's API?"
  It reads a file.
- **Portability.** The spec directory is the complete, self-contained
  state of the system. Copy it to another machine, run the tools, get
  the same result.

The spec directory is the system's API. The programs are its
implementations.

---

## 8. Observation vs Assertion

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

## 9. The ChangeSet Is the Universal Contract

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

## 10. Summary

| Principle | One-Line Rule |
|-----------|---------------|
| One level of abstraction per program | vmlab realizes the topology, newtron translates specs to config, orchestrators decide what gets applied where |
| Objects own their methods | A method belongs to the smallest object that has all the context to execute it |
| If you change it, you verify it | The tool that writes the state must be able to verify the write |
| Spec vs config | Intent is declarative and version-controlled; state is imperative and generated |
| DRY across programs | Every capability exists in exactly one place, even across program boundaries |
| Dry-run as first-class | Default to preview; execution is opt-in; this forces clean separation of compute and apply |
| Files, not APIs | Programs communicate through the spec directory, not through shared libraries or services |
| Observation vs assertion | Assert your own work; observe everything else as structured data |
| The ChangeSet is universal | One contract for all mutations; one verification method for all operations |
