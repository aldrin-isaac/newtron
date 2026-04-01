package node

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// QoS Operations (Per-Interface)
// ============================================================================

// ApplyQoS applies a QoS policy to this interface (device-wide maps + per-interface bindings).
func (i *Interface) ApplyQoS(ctx context.Context, policyName string, policy *spec.QoSPolicy) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition(sonic.OpApplyQoS, i.name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpApplyQoS)
	if err := i.ensureInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpApplyQoS, "interface|"+i.name+"|qos",
		map[string]string{sonic.FieldQoSPolicy: policyName},
		[]string{"interface|" + i.name}); err != nil {
		return nil, err
	}
	cs.ReverseOp = "interface.remove-qos"
	cs.OperationParams = map[string]string{"interface": i.name}

	// Generate device-wide entries (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE)
	cs.Adds(GenerateDeviceQoSConfig(policyName, policy))

	// Generate per-interface entries (PORT_QOS_MAP, QUEUE)
	cs.Adds(bindQosConfig(i.name, policyName, policy))

	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Applied QoS policy '%s' to interface %s", policyName, i.name)
	return cs, nil
}

// RemoveQoS removes QoS configuration from this interface.
// If this is the last interface referencing the QoS policy, device-wide entries
// (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE) are also removed.
func (i *Interface) RemoveQoS(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-qos", i.name).Result(); err != nil {
		return nil, err
	}

	// Read policy name from intent — not from CONFIG_DB
	intentKey := "interface|" + i.name + "|qos"
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no QoS intent for %s", i.name)
	}
	policyName := intent.Params[sonic.FieldQoSPolicy]

	// Resolve spec for queue count — needed for deterministic unbind
	var queueCount int
	var policy *spec.QoSPolicy
	if policyName != "" {
		policy, _ = n.GetQoSPolicy(policyName)
		if policy != nil {
			queueCount = len(policy.Queues)
		}
	}

	cs := buildChangeSet(n.Name(), "interface.remove-qos", unbindQosConfig(i.name, queueCount), ChangeDelete)

	// Clean up device-wide entries if no other interface references this policy
	if policyName != "" && !n.isQoSPolicyReferenced(policyName, i.name) {
		cs.Deletes(deleteDeviceQoSConfig(policyName, policy))
	}

	if err := i.node.deleteIntent(cs, "interface|"+i.name+"|qos"); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Removed QoS from interface %s", i.name)
	return cs, nil
}

