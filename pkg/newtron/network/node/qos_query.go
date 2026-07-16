// qos_query.go contains QoS intent scan helpers and spec utilities.
package node

import (
	"strconv"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

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
// member at apply/join time (refuseTrunkOnPolicyVLAN, §7), because a per-port
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

// deleteMemberQoSRows deletes a member's PORT_QOS_MAP/QUEUE rows. The queue count is
// read from the projection (device reality), not re-resolved from a spec.
func (n *Node) deleteMemberQoSRows(cs *ChangeSet, member string) {
	queueCount := 0
	for key := range n.configDB.Queue {
		if strings.HasPrefix(key, member+"|") {
			queueCount++
		}
	}
	if queueCount > 0 || n.configDB.PortQoSMap[member].DSCPToTCMap != "" {
		cs.Deletes(unbindQosConfig(member, queueCount))
	}
}

// unbindMemberQoS removes a leaving member's QoS rows when no irb-type service
// still binds it — the reverse of bindMemberQoS (§15).
// A member leaving its last serviced VLAN loses its PORT_QOS_MAP/QUEUE; one that
// still belongs to another serviced VLAN keeps them. Called after the membership
// intent is deleted, so the left VLAN's binding is already gone.
func (n *Node) unbindMemberQoS(cs *ChangeSet, member string) {
	if n.isMemberServiceQoSBound(member, "") {
		return
	}
	n.deleteMemberQoSRows(cs, member)
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
