// qos_query.go contains QoS intent scan helpers and spec utilities.
package node

import (
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// isQoSPolicyReferenced checks if any QoS intent (excluding the given interface)
// references the policy. Scans both standalone QoS intents (interface|X|qos) and
// service intents (interface|X with qos_policy param).
func (n *Node) isQoSPolicyReferenced(policyName, excludeInterface string) bool {
	for resource, intent := range n.IntentsByPrefix("interface|") {
		// Standalone QoS intents: "interface|Ethernet0|qos"
		if strings.HasSuffix(resource, "|qos") && intent.Params[sonic.FieldQoSPolicy] == policyName {
			parts := strings.SplitN(resource, "|", 3)
			if len(parts) >= 2 && parts[1] != excludeInterface {
				return true
			}
		}
		// Service intents with QoS: "interface|Ethernet0" (OpApplyService with qos_policy)
		if intent.Operation == sonic.OpApplyService && intent.Params["qos_policy"] == policyName {
			parts := strings.SplitN(resource, "|", 2)
			if len(parts) == 2 && parts[1] != excludeInterface {
				return true
			}
		}
	}
	return false
}

// parsePolicyName extracts the policy name from a PORT_QOS_MAP bracket-ref
// like "[DSCP_TO_TC_MAP|myPolicy]" → "myPolicy". Returns "" if not a bracket-ref.
func parsePolicyName(bracketRef string) string {
	const prefix = "[DSCP_TO_TC_MAP|"
	if !strings.HasPrefix(bracketRef, prefix) || !strings.HasSuffix(bracketRef, "]") {
		return ""
	}
	return bracketRef[len(prefix) : len(bracketRef)-1]
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
