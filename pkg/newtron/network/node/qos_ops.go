package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// QoS Operations (Per-Interface)
// ============================================================================

// ApplyQoS applies a QoS policy to a specific interface (surgical override).
func (n *Node) ApplyQoS(ctx context.Context, intfName, policyName string, policy *spec.QoSPolicy) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)

	if err := n.precondition("apply-qos", intfName).Result(); err != nil {
		return nil, err
	}

	if !n.InterfaceExists(intfName) {
		return nil, fmt.Errorf("interface %s does not exist", intfName)
	}

	cs := NewChangeSet(n.name, "device.apply-qos")

	// Generate device-wide entries (DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER, WRED_PROFILE)
	deviceEntries := GenerateQoSDeviceEntries(policyName, policy)
	for _, entry := range deviceEntries {
		cs.Add(entry.Table, entry.Key, ChangeAdd, nil, entry.Fields)
	}

	// Generate per-interface entries (PORT_QOS_MAP, QUEUE)
	intfEntries := generateQoSInterfaceEntries(policyName, policy, intfName)
	for _, entry := range intfEntries {
		cs.Add(entry.Table, entry.Key, ChangeAdd, nil, entry.Fields)
	}

	util.WithDevice(n.name).Infof("Applied QoS policy '%s' to interface %s", policyName, intfName)
	return cs, nil
}

// RemoveQoS removes QoS configuration from a specific interface.
func (n *Node) RemoveQoS(ctx context.Context, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)

	if err := n.precondition("remove-qos", intfName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.remove-qos")

	// Find and remove QUEUE entries for this interface
	if n.configDB != nil {
		prefix := intfName + "|"
		for key := range n.configDB.Queue {
			if strings.HasPrefix(key, prefix) {
				cs.Add("QUEUE", key, ChangeDelete, nil, nil)
			}
		}
	}

	// Remove PORT_QOS_MAP entry for this interface
	if n.configDB != nil {
		if _, ok := n.configDB.PortQoSMap[intfName]; ok {
			cs.Add("PORT_QOS_MAP", intfName, ChangeDelete, nil, nil)
		}
	}

	util.WithDevice(n.name).Infof("Removed QoS from interface %s", intfName)
	return cs, nil
}
