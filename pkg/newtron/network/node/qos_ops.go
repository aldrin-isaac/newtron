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

	if err := n.precondition("apply-qos", i.name).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface.apply-qos")

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

	configDB := n.ConfigDB()

	// Extract policy name before deleting per-interface entries
	var policyName string
	if configDB != nil {
		if entry, ok := configDB.PortQoSMap[i.name]; ok {
			policyName = parsePolicyName(entry.DSCPToTCMap)
		}
	}

	cs := buildChangeSet(n.Name(), "interface.remove-qos", i.unbindQos(), ChangeDelete)

	// Clean up device-wide entries if no other interface references this policy
	if policyName != "" && !n.isQoSPolicyReferenced(policyName, i.name) {
		cs.Deletes(n.deleteDeviceQoSConfig(policyName))
	}

	n.trackOffline(cs)
	util.WithDevice(n.Name()).Infof("Removed QoS from interface %s", i.name)
	return cs, nil
}

// ============================================================================
// Node convenience delegators — resolve interface name, delegate to Interface
// ============================================================================

// ApplyQoS applies a QoS policy to a named interface.
func (n *Node) ApplyQoS(ctx context.Context, intfName, policyName string, policy *spec.QoSPolicy) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)
	if !n.InterfaceExists(intfName) {
		return nil, fmt.Errorf("interface %s does not exist", intfName)
	}
	iface, err := n.GetInterface(intfName)
	if err != nil {
		return nil, err
	}
	return iface.ApplyQoS(ctx, policyName, policy)
}

// RemoveQoS removes QoS configuration from a named interface.
func (n *Node) RemoveQoS(ctx context.Context, intfName string) (*ChangeSet, error) {
	intfName = util.NormalizeInterfaceName(intfName)
	iface, err := n.GetInterface(intfName)
	if err != nil {
		return nil, err
	}
	return iface.RemoveQoS(ctx)
}
