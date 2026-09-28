package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// QoS Operations (Per-Interface)
// ============================================================================

// qosBindable reports whether an interface kind is a legal QoS bind point. The
// single owner of that question for the derived per-member paths (§25), where the
// port is discovered from VLAN membership rather than named by the operator: a
// PortChannel can be a VLAN member but holds no PORT_QOS_MAP row (LAG QoS is
// per-member in SONiC — see CapabilityQoSBinding, RCA-051), and the schema refuses
// the key outright. The operator-named paths need no such check; their forward
// capability gate already refuses a bind the reverse could then find.
func qosBindable(intfName string) bool {
	return interfaceKindOf(intfName).HasCapability(CapabilityQoSBinding)
}

// qosBindingKey returns the intent resource key for an interface's QoS binding —
// a sub-resource of the interface's identity record (interface|<name>), and the
// single owner of this key so writer, readers, and teardown cannot diverge (§25).
func qosBindingKey(intfName string) string {
	return "interface|" + intfName + "|qos"
}

// createQoSBindingIntent writes the interface|<name>|qos intent record. It is the
// single owner of that record's contents (§25/§27), shared by the standalone
// BindQoS and the service-derived bind ApplyService performs — the QoS twin of
// createACLIntent, converged for the same reason (#492/#494).
//
// It is deliberately NOT called on the irb path: an irb service's QoS is
// delivered per VLAN member and must not be recorded, because a bind-qos intent
// on an IRB is unreplayable (the capability gate refuses it) and would abort the
// next projection rebuild. That is a caller's decision, expressed by not calling
// this — never by a flag here.
func (i *Interface) createQoSBindingIntent(cs *ChangeSet, policyName string) error {
	return i.node.writeIntent(cs, sonic.OpBindQoS, qosBindingKey(i.name),
		map[string]string{sonic.FieldQoSPolicy: policyName},
		[]string{"interface|" + i.name})
}

// deleteQoSBindingIntent removes the interface|<name>|qos intent record — the
// reverse of createQoSBindingIntent, named with it and shipped with it (§15).
// Shared by the standalone UnbindQoS and RemoveService's teardown.
func (i *Interface) deleteQoSBindingIntent(cs *ChangeSet) error {
	return i.node.deleteIntent(cs, qosBindingKey(i.name))
}

// BindQoS binds a QoS policy to this interface — creates device-wide
// maps (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE) on
// first reference and per-interface entries (PORT_QOS_MAP, QUEUE) every
// time. §24: shared device-wide policy lifecycle, last-consumer cleanup
// on UnbindQoS. §16 verb vocabulary: bind/unbind, mirror of BindACL.
func (i *Interface) BindQoS(ctx context.Context, policyName string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition(sonic.OpBindQoS, i.name).Result(); err != nil {
		return nil, err
	}
	// Resolve after the gate: operations accept names and resolve specs
	// internally (§33) — and a structurally-refused bind should never 404
	// on the spec first.
	policy, err := n.GetQoSPolicy(policyName)
	if err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpBindQoS)
	if err := i.createInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.createQoSBindingIntent(cs, policyName); err != nil {
		return nil, err
	}
	cs.ReverseOp = "interface." + sonic.OpUnbindQoS
	cs.OperationParams = map[string]string{"interface": i.name}

	// Generate device-wide entries (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE)
	cs.Adds(GenerateDeviceQoSConfig(policyName, policy))

	// Generate per-interface entries (PORT_QOS_MAP, QUEUE)
	cs.Adds(bindQosConfig(i.name, policyName, policy))

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Bound QoS policy '%s' to interface %s", policyName, i.name)
	return cs, nil
}

// UnbindQoS unbinds the QoS policy from this interface. If this is the
// last interface referencing the policy, the device-wide entries
// (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE) are also
// removed (§24 last-consumer cleanup).
func (i *Interface) UnbindQoS(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition(sonic.OpUnbindQoS, i.name).Result(); err != nil {
		return nil, err
	}

	// Read policy name from intent — not from CONFIG_DB
	intentKey := qosBindingKey(i.name)
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no QoS intent for %s", i.name)
	}
	policyName := intent.Params[sonic.FieldQoSPolicy]

	// No spec is resolved here: the policy name is the decision this record holds,
	// and the rows to clear follow from it plus the namespace bound (§20 — a reverse
	// is self-sufficient from the record; §15 — it clears what it owns).
	cs := buildChangeSet(n.Name(), "interface."+sonic.OpUnbindQoS, unbindQosConfig(i.name), ChangeDelete)

	// Clean up device-wide entries if no other interface references this policy
	if policyName != "" && !n.isQoSPolicyReferenced(policyName, i.name) {
		cs.Deletes(deleteDeviceQoSConfig(policyName))
	}

	if err := i.deleteQoSBindingIntent(cs); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Unbound QoS from interface %s", i.name)
	return cs, nil
}

// ============================================================================
// QoS Per-Member Derivation (IRB delivery-point) + policy-reference scans
// ============================================================================
//
// An irb service's QoS cannot bind to the IRB itself (§7), so it is derived per
// VLAN member by scanning membership + service bindings and never recorded (§21)
// — the QoS twin of the per-member ACL derivation in acl_ops.go. These helpers
// own that derivation and the "is this policy/port still bound?" scans that both
// the membership-leave path and RemoveService/UnbindQoS consult.

// vlanMembershipsOf returns the VLAN IDs a member port belongs to (access +
// trunk), read from the membership intents.
func (n *Node) vlanMembershipsOf(member string) []int {
	var vlans []int
	seen := map[int]bool{}
	for resource, intent := range n.IntentsByPrefix("interface|") {
		if resourceInterfaceName(resource) != member {
			continue
		}
		switch intent.Operation {
		case sonic.OpConfigureInterface, sonic.OpAddTrunkVLAN:
			if v, _ := strconv.Atoi(intent.Params[sonic.FieldVLANID]); v > 0 && !seen[v] {
				seen[v] = true
				vlans = append(vlans, v)
			}
		}
	}
	return vlans
}

// bindMemberQoS binds an irb-type service's QoS to the VLAN's member ports —
// where an irb service's per-member QoS lives, since the IRB itself is no QoS
// bind point (§7). The bound rows are derived from the service binding and the
// VLAN membership, never recorded (§21). QoS is per-port (PORT_QOS_MAP + QUEUE),
// so unlike the ACL ports-list it is one row per member. Every member is
// single-VLAN — a QoS-bearing irb service is refused on a VLAN with any trunk
// member at apply/join time (refuseUndeliverablePolicy, §7), because a per-port
// PORT_QOS_MAP on a trunk member would bleed to the trunk's other VLANs. So here
// the per-port map is exactly the per-VLAN policy; there is no conflict to check.
// Idempotent — safe to call whenever membership changes.
func (n *Node) bindMemberQoS(cs *ChangeSet, vlanID int) {
	for resource, intent := range n.IntentsByParam(sonic.FieldVLANID, strconv.Itoa(vlanID)) {
		if intent.Operation != sonic.OpApplyService {
			continue
		}
		if interfaceKindOf(resourceInterfaceName(resource)) != KindIRB {
			continue
		}
		policyName := intent.Params["qos_policy"]
		if policyName == "" {
			continue
		}
		policy, err := n.GetQoSPolicy(policyName)
		if err != nil || policy == nil {
			continue // orphaned policy reference — skip, like the ACL path
		}
		for _, member := range n.vlanMemberPorts(vlanID) {
			cs.Adds(bindQosConfig(member, policyName, policy))
		}
	}
}

// isMemberServiceQoSBound reports whether any irb-type service (other than the
// excludeKey, "" to exclude none) binds QoS to this member through a VLAN it
// belongs to. The single owner of "is QoS still bound on this port by a service?"
// (§25) — consulted by both the membership-leave path and RemoveService.
func (n *Node) isMemberServiceQoSBound(member, excludeKey string) bool {
	for _, vlanID := range n.vlanMembershipsOf(member) {
		for resource, intent := range n.IntentsByParam(sonic.FieldVLANID, strconv.Itoa(vlanID)) {
			if resource == excludeKey {
				continue
			}
			if intent.Operation == sonic.OpApplyService &&
				interfaceKindOf(resourceInterfaceName(resource)) == KindIRB &&
				intent.Params["qos_policy"] != "" {
				return true
			}
		}
	}
	return false
}

// deleteMemberQoSRows clears a member's QoS rows. The single owner of the
// per-member reverse (§25), shared by the membership-leave path and RemoveService.
//
// It reads neither the policy spec nor the projection. The former is not what was
// applied and the latter is the same re-derivation (see unbindQosConfig); what
// bounds the delete is the namespace, and what gates it is the member's kind — a
// PortChannel VLAN member holds no PORT_QOS_MAP row and the schema refuses the
// key, so emitting one would fail the whole operation. That gate is why this is a
// function and not an inlined call.
func (n *Node) deleteMemberQoSRows(cs *ChangeSet, member string) {
	if !qosBindable(member) {
		return
	}
	cs.Deletes(unbindQosConfig(member))
}

// unbindMemberQoS removes a leaving member's QoS rows — the reverse of
// bindMemberQoS (§15). A member leaving its last serviced VLAN loses its
// PORT_QOS_MAP/QUEUE; one that still belongs to another serviced VLAN keeps them.
//
// leftVLANs are the VLANs the member is leaving. They must be passed in: this runs
// after the membership intents are deleted, so the left VLANs are no longer
// derivable from the member, and without them a port leaving a VLAN that never
// carried QoS would emit a teardown for rows it never had. Both gates read intent
// records — one asks whether any *remaining* service still binds QoS here, the
// other whether any *departed* VLAN's service bound it at all.
func (n *Node) unbindMemberQoS(cs *ChangeSet, member string, leftVLANs map[int]bool) {
	if n.isMemberServiceQoSBound(member, "") {
		return
	}
	for vlanID := range leftVLANs {
		if b := n.GetIntent(bindingKey(VLANName(vlanID))); b != nil && b.Params["qos_policy"] != "" {
			n.deleteMemberQoSRows(cs, member)
			return
		}
	}
}

// isQoSPolicyReferenced checks if any QoS intent (excluding the given interface)
// references the policy. Scans both standalone QoS intents (interface|X|qos) and
// service intents (interface|X with qos_policy param).
func (n *Node) isQoSPolicyReferenced(policyName, excludeInterface string) bool {
	for resource, intent := range n.IntentsByPrefix("interface|") {
		// Standalone QoS intents: "interface|Ethernet0|qos"
		if strings.HasSuffix(resource, "|qos") && intent.Params[sonic.FieldQoSPolicy] == policyName {
			if name := resourceInterfaceName(resource); name != "" && name != excludeInterface {
				return true
			}
		}
		// Service binding with QoS: "interface|Ethernet0|service" (OpApplyService with qos_policy)
		if intent.Operation == sonic.OpApplyService && intent.Params["qos_policy"] == policyName {
			if name := resourceInterfaceName(resource); name != "" && name != excludeInterface {
				return true
			}
		}
	}
	return false
}

// GetServiceQoSPolicy returns the QoS policy name and definition for a service.
// Returns ("", nil) if no QoS policy is set.
func GetServiceQoSPolicy(sp SpecProvider, svc *spec.ServiceSpec) (string, *spec.QoSPolicy) {
	if svc.QoSPolicy != "" {
		if policy, err := sp.GetQoSPolicy(svc.QoSPolicy); err == nil {
			return svc.QoSPolicy, policy
		}
	}
	return "", nil
}

