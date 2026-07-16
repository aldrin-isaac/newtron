# IRB Service Redesign — The Gateway Is the Point of Service

**Status:** Ratified. The two design-principle amendments (§10) are applied
to both `DESIGN_PRINCIPLES.md` (concept) and `DESIGN_PRINCIPLES_NEWTRON.md`
(application) as of 2026-07-14; implementation proceeds by the phasing in §11.

**Prerequisite, landed:** the interface-kind capability model (`interface_kind.go`),
the per-kind capability gates, `update-irb`, and the SVI single-author guards
(PRs #432, #433). This redesign builds on that substrate — it does not redo it.

---

## 1. The problem: a routed service with no gateway to deliver to

Every service-delivery system must choose what it binds to, and that choice
becomes its unit of lifecycle, its unit of state, and its blast radius
(`DESIGN_PRINCIPLES_NEWTRON.md` §6). newtron binds to the interface, and for a
routed service on a physical port that is exactly right: the port is where the
L3 identity lives, the port is where the BGP session's update-source is, the
port is the one thing that fails.

An IRB service breaks the choice. An IRB — Integrated Routing and Bridging, the
`VlanN` SVI, the L3 gateway for a bridge domain — is a routed service whose
delivery point is *the gateway*, not any of the access ports that happen to
carry the VLAN. Today newtron has no gateway to bind to, so it binds the service
to each access port instead and smears one logical service across N bindings.
The consequences are not hypothetical; they were measured (see the arc that
produced this document):

- **Refreshing a shared property is neither one binding nor all of them.**
  Change a service's anycast gateway IP with ten access ports bound, and a
  per-binding `refresh-service` on the first port re-renders that port's rows —
  but the `VLAN_INTERFACE` gateway sub-entry is keyed by the IP, its delete
  lives in the last-member branch, and under a rolling refresh the branch is
  never reached. Both gateway IPs end up live on the device, indefinitely.
- **VLAN-level infrastructure never converges.** `CreateVLAN` is
  intent-idempotent, so a changed L2VNI mapping is skipped on every refresh; the
  zero-consumer point that would force re-creation is unreachable mid-rollout.
  The only path that converges these today is remove-all-ten-then-apply-all-ten
  — a full service outage.
- **The SVI has two authors.** `configure-irb` writes the gateway; an irb-type
  `apply-service` also writes it (`createSviConfig`, per access binding). Two
  writers of one `VLAN_INTERFACE` row, guarded today only by the refusals added
  in #433 — a guard is a smaller thing to need than a model where the conflict
  cannot arise.

These are one root cause wearing three faces: **the service's true point of
delivery was never made a first-class interface.**

---

## 2. Two anchors, both real

The instinct that resolves this is to stop looking for a single anchor. A
serviced bridge domain has two, and they answer different questions:

- **The VLAN is the membership container.** It answers *which ports attach to
  this bridge domain* — a topology-flavored fact, service-agnostic, exactly like
  LAG membership. A port joins a VLAN with no knowledge of whether that VLAN is
  IRB-enabled; that ignorance is what makes membership churn safe.
- **The IRB is the point of service delivery.** It answers *what this domain
  means at L3* — the gateway identity, the routing, the policy. An IRB service
  binds here, once.

Neither subsumes the other. Membership without a gateway is a pure-L2 bridge
domain; a gateway without members is an SVI waiting for traffic. The redesign
keeps both and gives each its own lifecycle verbs — most of which already exist.

---

## 3. The IRB is a first-class delivery interface

The `VlanN` interface — a software object standing for a VLAN's L3 gateway, not
the VLAN itself — becomes a delivery point that an irb-type service binds to
directly, the same way a routed service binds to a physical port or a LAG. The
model is the PortChannel's, exactly:

| | PortChannel | IRB (VlanN) |
|---|---|---|
| Interface created a priori | `create-portchannel` | `create-vlan` + `configure-irb` |
| Identity update in place | `update-portchannel`-family | `update-irb` (landed, #433) |
| Members join/leave | `add`/`remove-portchannel-member` | `configure-interface` bridged, `add-trunk-vlan` |
| Service binds once | `apply-service PortChannel1` | `apply-service Vlan100` |
| Members individually serviceable | refused — "configure the PortChannel instead" | access ports take membership only |

Most of this exists. `configure-irb` / `update-irb` / `unconfigure-irb` are
already the IRB's create/update/destroy lifecycle; membership operations are
already first-class and service-independent. The redesign is not "build an IRB
object" — it is **move the irb-type service binding to the IRB, and strip
membership and SVI authorship out of the per-access-port apply path.**

This restores an invariant the current code quietly violates: *`apply-service`
never creates its own delivery interface.* A physical port exists by hardware; a
PortChannel by creation; and now an IRB by `configure-irb` — with the service
refusing to bind until it exists. The same LAG rule, applied to the gateway.

---

## 4. Per-member policy — how a filter/QoS reaches the members

An irb-type service still has per-access-port content: a filter or QoS or storm
control that must reach every member port, not just the gateway. Under the old
model each access binding carried its own copy. Copies go stale the instant the
service changes — the same convergence disease, now with a copier — and they are
order-dependent (the "what if the IRB is created after the members?" question is
the tell: any model whose outcome depends on arrival order is a hidden state
machine).

The redesign **derives** the per-member policy instead of copying it. The IRB is
no ACL/QoS bind point (§7), so an irb service's filter/QoS is **bound to the
VLAN's member ports**, and that binding is a derived view of two facts:

> A member port carries the policy **iff** the service is bound on the IRB
> **and** the port is a member of the VLAN. The binding is (re)derived by
> whichever fact arrives second.

- Member joins after the service is bound → bind the policy to it at join.
- Service binds after members exist → bind it to the existing members.
- Member leaves, or the service unbinds → the binding for that member goes; the
  rest persist.

The per-member bindings are **derived, never recorded**
(`DESIGN_PRINCIPLES_NEWTRON.md` §21 — Reconstruct, Don't Record). Materializing a
per-member derived intent would store the *join of two other intents*, which can
go stale inside the intent DB — the precise drift §1 exists to kill, reintroduced
in the substrate. Instead one owner computes the bindings, invoked from three
sites with one implementation (§27, and its mechanical check `ai-instructions.md`
§25):

1. The binding operations (`apply`/`refresh-service`) — bind over the VLAN's
   current members.
2. The membership operations (join/leave) — bind/unbind the one member.
3. Reconstruction — a post-replay reconcile pass. Replay rebuilds the intent DB
   incrementally, so a per-step binding computed mid-replay can see a
   partially-loaded DB; once every intent is loaded, the reconcile pass
   recomputes each service ACL's bound ports from the complete DB, so the result
   is order-independent regardless of the order the steps replayed in.

The ten-binding refresh dissolves: one binding on the IRB, one `refresh-service`
call, and binding the policy to each member is internal to the single owner. The
gateway identity converges in that one call with no ghost, because there is now
one binding whose teardown-replace owns the sub-entry.

---

## 5. Intent records and the DAG

The redesign changes the intent DAG in one predicted place and one that is less
obvious.

**Binding re-keys to a sub-resource, uniformly for every kind.** Today the
binding *is* the `interface|<name>` record when its operation is `apply-service`,
and the same key holds an `interface-init` record when only property or ACL
operations touched the interface — one resource, two meanings. On an IRB this
collides outright: `interface|Vlan100` is already the `configure-irb` identity
record, so the binding physically cannot live there. The fix is uniform: the
identity record is always `interface|<name>`, and the service binding is always
a child record `interface|<name>|service`, joining the existing sub-resource
family (`…|acl|ingress`, `…|qos`, `…|bgp-peer`). Same concept, same name, every
kind (§13). This re-keys persisted intents — a data migration, addressed in §11.

**The DAG's edges change, and the lifecycle rule falls out of invariant I5**
(a parent with children cannot be deleted — `intent-dag-architecture.md` §3):

```
device
  └── vlan|100                         (create-vlan)
        ├── interface|EthernetN         (membership; vlan param)   [access members]
        ├── interface|Vlan100           (configure-irb; +vrf|X)    [the IRB identity]
        │     └── interface|Vlan100|service   (apply-service)      [the one binding]
        └── ...
```

The VLAN is destroyable only when memberless *and* IRB-less; the IRB only when
unbound; access members join and leave freely because nothing parents to them.
No new machinery — the existing bottom-up deletion invariant already enforces
every rule the two anchors need.

**The per-member policy rows are not in this tree.** They are projection-level, recomputed
per §4 — the DAG records decisions, not their derivations.

---

## 6. Content partition — which fields land where

An irb-type service's spec fields split by where they are realized. Naming the
split is the design work; the split is not optional, because a field that could
land on either side has no owner.

| Field class | Realized on | Example |
|---|---|---|
| Gateway identity | the IRB (`VLAN_INTERFACE` base + IP) | anycast IP, VRF binding, anycast MAC |
| Overlay realization | VLAN-level infrastructure | L2VNI mapping, route targets, ARP suppression |
| Per-member policy | each access port (derived per-member, §4) | ingress/egress filter, QoS, storm control |

Gateway identity is the IRB's own (`configure-irb` authors it; `update-irb`
mutates it in place). Overlay realization is VLAN-scoped and converges through
the binding's render, not through `CreateVLAN`'s idempotent skip. Per-member
policy is derived per-member (§4). One consequence to ratify explicitly: with the binding on
the IRB, `configure-irb` becomes the **sole** author of the SVI — the #433
dual-writer guard becomes structural rather than defensive, because the service
layers policy on top of an SVI it no longer creates.

---

## 7. Policy scope — vlan-scoped, declared, not approximated

SONiC cannot bind an ACL or a QoS map to a VLAN interface. Verified against the
pinned 202511 tree: `sonic-acl.yang`'s `ports` leaf-list is a union of leafrefs
to `PORT` and `PORTCHANNEL` only, and `aclorch.cpp`'s bind-point table
(`aclBindPointTypeLookup`) has exactly those two entries; `PORT_QOS_MAP`'s
`ifname` key is `global` or a leafref to `PORT`. SAI defines
`SAI_ACL_BIND_POINT_TYPE_VLAN`, but SONiC never wired it. So "bind at the irb,
applies to the bridge domain" — the Junos model — is not natively expressible.

The feasible realization is deriving per-member-port rows via
the standard tables — abstraction on top of the community mechanism, not a
replacement for it (Platform Patching Principle, §37). One native assist:
`aclorch.cpp` maps `MATCH_VLAN_ID` to `SAI_ACL_ENTRY_ATTR_FIELD_OUTER_VLAN_ID`,
so projected ACL *rules* can be VLAN-qualified — a trunk member carrying two
service-VLANs receives the union of two vlan-scoped rule sets rather than a
collision.

Two honest limits the design must state, not paper over:

- **The policy is vlan-scoped, not gateway-scoped.** A Junos irb filter touches
  only traffic crossing the L3 hop; per-member-port projection also catches
  intra-VLAN east-west traffic that never reaches the gateway. That is a
  different policy scope. The service field means *vlan-scoped policy* — named
  as such — not an approximation of gateway filtering.
- **QoS on a shared trunk port is unsolvable and must fail closed.** QoS maps
  are per-port with no VLAN qualifier, so two serviced VLANs with different QoS
  policies bound to one trunk member cannot both be honored. The
  binding refuses the conflict before writing, naming both services (§13: prevent,
  don't detect).

---

## 8. Out of scope, with reasons

- **Bridged / evpn-bridged services stay per-access-port.** They have no L3
  gateway, so there is no delivery interface to bind to; their content genuinely
  is per-port. Whether a pure-L2 VLAN eventually deserves a VLAN-anchored variant
  of this per-member model is a real open question — it must get its own answer, not
  inherit the irb answer by assumption.
- **The saga executor** remains deferred (`memory/project_saga_design.md`): built
  as newtrun's model pointed at production when a consumer exists, never as a
  config-only engine.
- **A device-scoped `refresh-service <name>`** (one call re-applies every
  binding of a service on a node) is a thin convenience over the per-binding
  primitive. It does not change the model and is not required here.

---

## 9. Migration

Greenfield rules apply (`DESIGN_PRINCIPLES_NEWTRON.md` §40): no compatibility
shims, no dual-key readers. Two things break for devices provisioned on older
binaries, and both converge through Reconcile:

- **The binding re-key** (§5) invalidates persisted `interface|<name>`
  apply-service records — they do not replay under the new registry. Labs
  reprovision; there is no production fleet.
- **The irb-service delivery-point flip** (§3) means old per-access-port irb
  bindings no longer describe the model. A destroy + redeploy on new binaries
  authors the new tree.

The round-trip registry, the suites (44-evpn-irb, 3node-ngdp-dataplane's
evpn-l2-irb, the 2node irb scenarios), and the §38 device validation all follow
the model change — they are the implementation's cost, not a separate migration.

---

## 10. DESIGN_PRINCIPLES amendments (require ratification)

The redesign is compliant with the design principles except at two points, where
it needs the principles themselves refined. Both are **ratified and applied**
(2026-07-14) — to `DESIGN_PRINCIPLES.md` as concept and
`DESIGN_PRINCIPLES_NEWTRON.md` as application, per the dual-doc contract. The
wording below records what was decided.

### 10.1 §20 — joint reconstruction from the intent DB

**Current:** "intent records must be self-sufficient for reconstruction of
expected state." The wording speaks of per-record sufficiency.

**Tension:** per-member policy reconstructs a member's rows from *sibling
intents* (the membership records), not from that binding record alone — and not
from specs.

**Proposed refinement:** *records are individually self-sufficient for their own
reverse; derived projection state may be reconstructed from the intent DB
jointly, never from specs.* Reading the intent DB is the decision substrate doing
its job (§5), not the spec re-resolution §20 forbids. The round-trip test already
enforces joint reconstruction — it replays the whole DB — so the amendment
describes enforced reality rather than loosening a guarantee.

### 10.2 §6 — a container carve-out to the isolation clause

**Current:** "services on Ethernet0 and Ethernet4 are independent" (the unit of
isolation).

**Tension:** under per-member policy, a member port's rows are partially
derived from its VLAN's service. Members of a serviced VLAN are not fully
independent.

**Proposed carve-out:** *an interface that joins a container (a LAG, a serviced
VLAN) cedes the container-scoped portion of its state to the container's owner;
its remaining state stays independent.* This generalizes a precedent §6 already
contains — a PortChannel member cedes its configuration to the container
("configure the PortChannel instead") — rather than contradicting the isolation
principle.

---

## 11. Implementation phasing (for reference, not this document's deliverable)

This document is Phase B. The code phases that follow, once the amendments are
ratified:

- **C — Intent/DAG mechanics.** Binding re-key to `interface|<name>|service` for
  every kind; `interface|<name>` normalized to identity-only; registry + Replay +
  round-trip sequence updates; I5-driven lifecycle asserted by tests.
- **D — Per-member policy delivery.** Single-owner binding function (full / member-delta
  / replay entry points); `ACL_TABLE` ports-list changes as §48 in-place edits;
  VLAN_ID-qualified rule generation; QoS conflict fail-closed.
- **E — Content partition + delivery-point flip.** irb / evpn-irb deliverable
  only on `KindIRB` (a registry matrix change); `apply-service` requires the
  pre-existing IRB; the ownership split of §6 realized; `VLAN_MEMBER` gets one
  writer (the membership ops).
- **F — §38 validation battery.** VLAN_ID rule matching on tagged and
  untagged/PVID members, both platforms; live ACL ports-list rebind behavior;
  single-binding refresh convergence (the ghost-IP regression, inverted); suite
  rewrites, cold.
- **G — Wire, docs, closure.** api.md; the pipeline and howto docs; CLAUDE.md
  ownership map; the §9 conformance audit; a full 13-suite sweep as the exit gate.

The suite workstream — an audit-first per-suite impact pass, new witnesses
(order-independence, single-call member binding, ghost-IP-inverted, member-leave
continuity, QoS conflict, the VLAN_ID battery), and two full-sweep checkpoints —
runs across C–G, not inside any one phase.

---

## 12. Open questions for review

1. **Ratify the two §10 amendments** as worded, or refine them.
2. **The content partition (§6)** — is the three-way split (gateway / overlay /
   per-member) the right cut, and does every current irb/evpn-irb spec field map
   cleanly onto one class?
3. **Bridged-service policy (§8)** — leave per-port for this redesign (proposed),
   or design the VLAN-anchored variant now?
4. **Policy scope (§7)** — ship vlan-scoped with the semantic declared (proposed),
   or invest in gateway-scoped approximation via dst-MAC rules?
