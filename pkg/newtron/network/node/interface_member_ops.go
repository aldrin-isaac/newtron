package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// LAG/VLAN Member Operations
// ============================================================================

// AddMember adds a member interface to this LAG or VLAN.
// For VLANs, the tagged parameter controls tagging mode.
func (i *Interface) AddMember(ctx context.Context, memberIntf string, tagged bool) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("add-member", i.name).Result(); err != nil {
		return nil, err
	}

	// Normalize member interface name (e.g., Eth0 -> Ethernet0)
	memberIntf = util.NormalizeInterfaceName(memberIntf)

	// Must be a PortChannel or VLAN
	if !i.IsPortChannel() && !i.IsVLAN() {
		return nil, fmt.Errorf("add-member only valid for PortChannel or VLAN interfaces")
	}

	// Validate member interface exists
	memberIntfObj, err := n.GetInterface(memberIntf)
	if err != nil {
		return nil, fmt.Errorf("member interface '%s' not found: %w", memberIntf, err)
	}

	cs := NewChangeSet(n.Name(), "interface.add-member")

	if i.IsPortChannel() {
		// LAG member addition
		// Member must be a physical interface
		if !memberIntfObj.IsPhysical() {
			return nil, fmt.Errorf("LAG members must be physical interfaces")
		}
		// Member must not already be in a LAG
		if memberIntfObj.IsLAGMember() {
			return nil, fmt.Errorf("interface %s is already a member of %s", memberIntf, memberIntfObj.LAGParent())
		}

		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeAdd, nil, map[string]string{})

		util.WithDevice(n.Name()).Infof("Added %s to LAG %s", memberIntf, i.name)

	} else if i.IsVLAN() {
		// VLAN member addition
		taggingMode := "untagged"
		if tagged {
			taggingMode = "tagged"
		}

		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		cs.Add("VLAN_MEMBER", memberKey, ChangeAdd, nil, map[string]string{
			"tagging_mode": taggingMode,
		})

		util.WithDevice(n.Name()).Infof("Added %s to VLAN %s (%s)", memberIntf, i.name, taggingMode)
	}

	return cs, nil
}

// RemoveMember removes a member interface from this LAG or VLAN.
func (i *Interface) RemoveMember(ctx context.Context, memberIntf string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-member", i.name).Result(); err != nil {
		return nil, err
	}

	// Normalize member interface name (e.g., Eth0 -> Ethernet0)
	memberIntf = util.NormalizeInterfaceName(memberIntf)

	// Must be a PortChannel or VLAN
	if !i.IsPortChannel() && !i.IsVLAN() {
		return nil, fmt.Errorf("remove-member only valid for PortChannel or VLAN interfaces")
	}

	configDB := n.ConfigDB()
	cs := NewChangeSet(n.Name(), "interface.remove-member")

	if i.IsPortChannel() {
		// LAG member removal
		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		if configDB != nil {
			if _, ok := configDB.PortChannelMember[memberKey]; !ok {
				return nil, fmt.Errorf("interface %s is not a member of %s", memberIntf, i.name)
			}
		}

		cs.Add("PORTCHANNEL_MEMBER", memberKey, ChangeDelete, nil, nil)

		util.WithDevice(n.Name()).Infof("Removed %s from LAG %s", memberIntf, i.name)

	} else if i.IsVLAN() {
		// VLAN member removal
		memberKey := fmt.Sprintf("%s|%s", i.name, memberIntf)
		if configDB != nil {
			if _, ok := configDB.VLANMember[memberKey]; !ok {
				return nil, fmt.Errorf("interface %s is not a member of %s", memberIntf, i.name)
			}
		}

		cs.Add("VLAN_MEMBER", memberKey, ChangeDelete, nil, nil)

		util.WithDevice(n.Name()).Infof("Removed %s from VLAN %s", memberIntf, i.name)
	}

	return cs, nil
}

// ============================================================================
// MAC-VPN (L2 EVPN) Operations
// ============================================================================

// BindMACVPN binds this VLAN interface to a MAC-VPN definition.
// This configures the L2VNI mapping and ARP suppression from the macvpn definition.
func (i *Interface) BindMACVPN(ctx context.Context, macvpnName string, macvpnDef *spec.MACVPNSpec) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("bind-macvpn", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("bind-macvpn only valid for VLAN interfaces")
	}
	if !n.VTEPExists() {
		return nil, fmt.Errorf("MAC-VPN requires VTEP configuration")
	}

	cs := NewChangeSet(n.Name(), "interface.bind-macvpn")

	vlanName := i.name // e.g., "Vlan100"

	// Add L2VNI mapping
	if macvpnDef.L2VNI > 0 {
		mapKey := fmt.Sprintf("vtep1|map_%d_%s", macvpnDef.L2VNI, vlanName)
		cs.Add("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vlan": vlanName,
			"vni":  fmt.Sprintf("%d", macvpnDef.L2VNI),
		})
	}

	// Configure ARP suppression
	if macvpnDef.ARPSuppression {
		cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeAdd, nil, map[string]string{
			"suppress": "on",
		})
	}

	util.WithDevice(n.Name()).Infof("Bound MAC-VPN '%s' to %s (L2VNI: %d)", macvpnName, vlanName, macvpnDef.L2VNI)
	return cs, nil
}

// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
// This removes the L2VNI mapping and ARP suppression settings.
func (i *Interface) UnbindMACVPN(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-macvpn", i.name).Result(); err != nil {
		return nil, err
	}
	if !i.IsVLAN() {
		return nil, fmt.Errorf("unbind-macvpn only valid for VLAN interfaces")
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-macvpn")

	vlanName := i.name
	configDB := n.ConfigDB()

	// Remove L2VNI mapping
	if configDB != nil {
		for key, mapping := range configDB.VXLANTunnelMap {
			if mapping.VLAN == vlanName {
				cs.Add("VXLAN_TUNNEL_MAP", key, ChangeDelete, nil, nil)
				break
			}
		}
	}

	// Remove ARP suppression
	if configDB != nil {
		if _, ok := configDB.SuppressVLANNeigh[vlanName]; ok {
			cs.Add("SUPPRESS_VLAN_NEIGH", vlanName, ChangeDelete, nil, nil)
		}
	}

	util.WithDevice(n.Name()).Infof("Unbound MAC-VPN from %s", vlanName)
	return cs, nil
}

// ============================================================================
// ACL Unbinding
// ============================================================================

// UnbindACL removes an ACL binding from this interface.
func (i *Interface) UnbindACL(ctx context.Context, aclName string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-acl", i.name).Result(); err != nil {
		return nil, err
	}

	configDB := n.ConfigDB()
	if configDB == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return nil, fmt.Errorf("ACL '%s' not found", aclName)
	}

	// Check if this interface is actually bound to this ACL
	ports := strings.Split(acl.Ports, ",")
	found := false
	for _, p := range ports {
		if strings.TrimSpace(p) == i.name {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("ACL '%s' is not bound to interface %s", aclName, i.name)
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-acl")
	depCheck := NewDependencyChecker(n, i.name)

	if depCheck.IsLastACLUser(aclName) {
		// Last user - delete all ACL rules and table
		prefix := aclName + "|"
		for ruleKey := range configDB.ACLRule {
			if strings.HasPrefix(ruleKey, prefix) {
				cs.Add("ACL_RULE", ruleKey, ChangeDelete, nil, nil)
			}
		}
		cs.Add("ACL_TABLE", aclName, ChangeDelete, nil, nil)
	} else {
		// Other users exist - just remove this interface from the binding list
		remainingBindings := depCheck.GetACLRemainingInterfaces(aclName)
		cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
			"ports": remainingBindings,
		})
	}

	// Update local state
	if acl.Stage == "ingress" {
		i.ingressACL = ""
	} else if acl.Stage == "egress" {
		i.egressACL = ""
	}

	util.WithDevice(n.Name()).Infof("Unbound ACL %s from interface %s", aclName, i.name)
	return cs, nil
}
