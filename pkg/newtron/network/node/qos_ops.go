package node

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ============================================================================
// QoS Operations (Per-Interface)
// ============================================================================

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
	if err := i.node.writeIntent(cs, sonic.OpBindQoS, "interface|"+i.name+"|qos",
		map[string]string{sonic.FieldQoSPolicy: policyName},
		[]string{"interface|" + i.name}); err != nil {
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

	cs := buildChangeSet(n.Name(), "interface."+sonic.OpUnbindQoS, unbindQosConfig(i.name, queueCount), ChangeDelete)

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
	util.WithDevice(n.Name()).Infof("Unbound QoS from interface %s", i.name)
	return cs, nil
}

