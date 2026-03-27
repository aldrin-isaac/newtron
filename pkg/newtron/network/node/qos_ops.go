package node

import (
	"context"
	"fmt"
	"strings"

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
	for _, entry := range GenerateDeviceQoSConfig(policyName, policy) {
		cs.Add(entry.Table, entry.Key, entry.Fields)
	}

	// Generate per-interface entries (PORT_QOS_MAP, QUEUE)
	for _, entry := range i.bindQos(policyName, policy) {
		cs.Add(entry.Table, entry.Key, entry.Fields)
	}

	util.WithDevice(n.Name()).Infof("Applied QoS policy '%s' to interface %s", policyName, i.name)
	return cs, nil
}

// unbindQos returns delete entries for QoS on this interface: QUEUE entries and PORT_QOS_MAP.
func (i *Interface) unbindQos() []sonic.Entry {
	configDB := i.node.ConfigDB()
	var entries []sonic.Entry

	// Find and remove QUEUE entries for this interface
	if configDB != nil {
		prefix := i.name + "|"
		for key := range configDB.Queue {
			if strings.HasPrefix(key, prefix) {
				entries = append(entries, sonic.Entry{Table: "QUEUE", Key: key})
			}
		}
	}

	// Remove PORT_QOS_MAP entry for this interface
	if configDB != nil {
		if _, ok := configDB.PortQoSMap[i.name]; ok {
			entries = append(entries, sonic.Entry{Table: "PORT_QOS_MAP", Key: i.name})
		}
	}

	return entries
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

	cs := buildChangeSet(n.Name(), "interface.remove-qos", i.unbindQos(), ChangeDelete)

	// Clean up device-wide entries if no other interface references this policy
	if policyName != "" && !n.isQoSPolicyReferenced(policyName, i.name) {
		cs.Deletes(n.deleteDeviceQoSConfig(policyName))
	}

	if err := i.node.deleteIntent(cs, "interface|"+i.name+"|qos"); err != nil {
		return nil, err
	}
	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Removed QoS from interface %s", i.name)
	return cs, nil
}

