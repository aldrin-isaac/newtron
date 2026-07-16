# RCA-051: ACL and QoS cannot bind to a VLAN/IRB interface — trunk members make per-member delivery undeliverable

**Severity**: Design-shaping. Not a crash — a hard SONiC limitation that dictates how an irb-type service may deliver a filter or QoS. Getting it wrong ships policy that silently half-applies.
**Platform**: Platform-independent (SONiC-wide). Verified against `sonic-net/sonic-swss` `orchagent/aclorch.cpp` on the pinned `202505` branch; the same constraint holds on sonic-vs (Force10-S6000) and CiscoVS (cisco-p200).
**Status**: **Worked around** — newtron delivers an irb-service filter/QoS to the VLAN's member ports and **fails closed** on any VLAN with a trunk (multi-VLAN) member. The real fix is upstream: SONiC wiring `SAI_ACL_BIND_POINT_TYPE_VLAN`. See `docs/newtron/irb-service-redesign.md` §7.

## Summary

An irb service's L3 delivery point is the IRB (`VlanN`, the SVI). The natural place to
bind a filter or QoS for the whole bridge domain is that interface — the Junos model.
**SONiC cannot do it.** So newtron delivers the policy to the VLAN's member ports
instead, which is only correct when a member carries exactly the one VLAN. On a trunk
member the delivery is undeliverable, and neither native trick rescues it. newtron
therefore refuses a filter/QoS-bearing irb service on a VLAN with any trunk member,
enforced from both directions (apply-service and membership-join).

## Root cause — three facts about aclorch/PORT_QOS_MAP

1. **No VLAN bind point.** `sonic-acl.yang`'s `ports` leaf-list is a union of leafrefs
   to `PORT` and `PORTCHANNEL` only; aclorch's built-in table types bind to
   `SAI_ACL_BIND_POINT_TYPE_PORT` / `_LAG` and nothing else. SAI *defines*
   `SAI_ACL_BIND_POINT_TYPE_VLAN`, but SONiC never wired it. `PORT_QOS_MAP`'s `ifname`
   key is `global` or a leafref to `PORT`. So a filter/QoS cannot bind to `VlanN`.

2. **The outer-VLAN match exists but is tag-keyed.** The built-in `L3` table type
   *does* carry `SAI_ACL_TABLE_ATTR_FIELD_OUTER_VLAN_ID` (unconditional in its
   `AclTableTypeBuilder`), and aclorch maps rule field `MATCH_VLAN_ID` →
   `SAI_ACL_ENTRY_ATTR_FIELD_OUTER_VLAN_ID`. So a *tagged* trunk member's traffic
   matches a VLAN-qualified rule. But the match keys on **the tag on the wire**: an
   **untagged (PVID-classified) member has no outer tag at the ingress-ACL stage**, so
   the match never fires — the filter silently misses that member. An outer-VLAN
   qualifier thus *requires* a VLAN match that untagged traffic cannot satisfy.

3. **QoS has no VLAN qualifier at all.** `PORT_QOS_MAP` is per-port, unconditional.
   A per-port QoS map on a trunk member applies to *every* VLAN on the trunk, not just
   the serviced one — it bleeds.

The tell: a per-member scheme "works" only for the all-tagged-single-VLAN case and
degrades to a silent no-op (filter) or over-application (QoS) the moment a member is
untagged or multi-VLAN.

## The workaround — deliver per-member on single-VLAN ports, fail closed on trunks

- On a **single-VLAN member** (access, or a single-VLAN trunk) per-port == per-VLAN:
  an *unqualified* L3 ACL matches only that VLAN's traffic (it is the only traffic on
  the port), and a per-port QoS map scopes to it exactly. Rules carry **no** VLAN_ID.
- A **filter/QoS-bearing irb service is refused** on a VLAN with any trunk (multi-VLAN)
  member — `refuseTrunkOnPolicyVLAN`, enforced symmetrically (§15): at `apply-service`
  (a trunk member present) and at membership-join (a policy member becoming a trunk).
  A *plain* irb service (no filter/QoS) is unaffected; trunk members are fine there.
- The dead end that was tried and backed out: qualifying every rule with `OUTER_VLAN_ID`
  ("a trunk member on two service-VLANs gets the union"). It only half-applied
  (untagged misses), so it was removed — rules are unqualified now.

## Resolution

Bind the filter/QoS to the IRB (`VlanN`) directly when SONiC wires
`SAI_ACL_BIND_POINT_TYPE_VLAN` for ACLs and a VLAN-qualified QoS bind point. That
collapses per-member delivery and the trunk refusal into a single VLAN-scoped bind —
one place to change (`serviceCapabilityNeeds` / the delivery-point code and §7).

## Verification

- aclorch source read on the pinned `202505` branch (bind-point table, the `L3` table's
  match set, the `MATCH_VLAN_ID` mapping).
- Cold, both platforms: the single-VLAN per-member ACL binds to the member with an
  **unqualified** rule (2node-vs / 2node-ngdp `service-lifecycle`, §38 witness).
- The trunk refusal (apply + join) is pinned by `TestMemberPolicy_TrunkGate` (unit) and
  the `1node-vs-config` `trunk-gate` loopback scenario.

## Related

- `docs/newtron/irb-service-redesign.md` §7 (policy scope) and §3 (the delivery-point flip).
- `DESIGN_PRINCIPLES_NEWTRON.md` §7 (definition network-scoped, execution device-scoped),
  §13 (prevent, don't detect), §37 (Platform Patching Principle — abstraction on the
  community mechanism, not a replacement).
- RCA-049 (the previous EVPN-wire RCA) — same discipline: read the daemon/orchagent
  source and verify against ground truth before blaming or working around the platform.
