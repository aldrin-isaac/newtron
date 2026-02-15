package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Interface Property Operations
// ============================================================================

// InterfaceConfig holds configuration options for Configure().
type InterfaceConfig struct {
	Description string
	MTU         int
	Speed       string
	AdminStatus string
}

// SetIP configures an IP address on this interface.
func (i *Interface) SetIP(ctx context.Context, ipAddr string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("set-ip", i.name).Result(); err != nil {
		return nil, err
	}
	if !util.IsValidIPv4CIDR(ipAddr) {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddr)
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure IP on LAG member")
	}

	cs := NewChangeSet(n.Name(), "interface.set-ip")
	ipKey := fmt.Sprintf("%s|%s", i.name, ipAddr)
	cs.Add("INTERFACE", ipKey, ChangeAdd, nil, map[string]string{})

	i.ipAddresses = append(i.ipAddresses, ipAddr)

	util.WithDevice(n.Name()).Infof("Configured IP %s on interface %s", ipAddr, i.name)
	return cs, nil
}

// SetVRF binds this interface to a VRF.
func (i *Interface) SetVRF(ctx context.Context, vrfName string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("set-vrf", i.name).Result(); err != nil {
		return nil, err
	}
	if vrfName != "" && vrfName != "default" && !n.VRFExists(vrfName) {
		return nil, fmt.Errorf("VRF '%s' does not exist", vrfName)
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot bind LAG member to VRF")
	}

	cs := NewChangeSet(n.Name(), "interface.set-vrf")
	cs.Add("INTERFACE", i.name, ChangeModify, nil, map[string]string{
		"vrf_name": vrfName,
	})

	i.vrf = vrfName

	util.WithDevice(n.Name()).Infof("Bound interface %s to VRF %s", i.name, vrfName)
	return cs, nil
}

// BindACL binds an ACL to this interface.
// ACLs are shared - adds this interface to the ACL's binding list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("bind-acl", i.name).Result(); err != nil {
		return nil, err
	}
	if !n.ACLTableExists(aclName) {
		return nil, fmt.Errorf("ACL table '%s' does not exist", aclName)
	}
	if direction != "ingress" && direction != "egress" {
		return nil, fmt.Errorf("direction must be 'ingress' or 'egress'")
	}

	cs := NewChangeSet(n.Name(), "interface.bind-acl")

	// ACLs are shared - add this interface to existing binding list
	configDB := n.ConfigDB()
	existingACL, ok := configDB.ACLTable[aclName]
	var newBindings string
	if ok && existingACL.Ports != "" {
		newBindings = addInterfaceToList(existingACL.Ports, i.name)
	} else {
		newBindings = i.name
	}

	cs.Add("ACL_TABLE", aclName, ChangeModify, nil, map[string]string{
		"ports": newBindings,
		"stage": direction,
	})

	if direction == "ingress" {
		i.ingressACL = aclName
	} else {
		i.egressACL = aclName
	}

	util.WithDevice(n.Name()).Infof("Bound ACL %s to interface %s (%s)", aclName, i.name, direction)
	return cs, nil
}

// Configure sets basic interface properties.
func (i *Interface) Configure(ctx context.Context, opts InterfaceConfig) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("configure", i.name).Result(); err != nil {
		return nil, err
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure LAG member directly")
	}

	if opts.MTU > 0 {
		if err := util.ValidateMTU(opts.MTU); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(n.Name(), "interface.configure")
	fields := make(map[string]string)

	if opts.Description != "" {
		fields["description"] = opts.Description
	}
	if opts.MTU > 0 {
		fields["mtu"] = fmt.Sprintf("%d", opts.MTU)
		i.mtu = opts.MTU
	}
	if opts.Speed != "" {
		fields["speed"] = opts.Speed
		i.speed = opts.Speed
	}
	if opts.AdminStatus != "" {
		fields["admin_status"] = opts.AdminStatus
		i.adminStatus = opts.AdminStatus
	}

	if len(fields) > 0 {
		cs.Add("PORT", i.name, ChangeModify, nil, fields)
	}

	util.WithDevice(n.Name()).Infof("Configured interface %s: %v", i.name, fields)
	return cs, nil
}

// ============================================================================
// Generic Property Setting
// ============================================================================

// Set sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description
func (i *Interface) Set(ctx context.Context, property, value string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("set-property", i.name).Result(); err != nil {
		return nil, err
	}
	if i.IsLAGMember() {
		return nil, fmt.Errorf("cannot configure LAG member directly - configure the parent LAG")
	}

	cs := NewChangeSet(n.Name(), "interface.set")
	fields := make(map[string]string)

	switch property {
	case "mtu":
		mtuVal := 0
		if _, err := fmt.Sscanf(value, "%d", &mtuVal); err != nil {
			return nil, fmt.Errorf("invalid MTU value: %s", value)
		}
		if err := util.ValidateMTU(mtuVal); err != nil {
			return nil, err
		}
		fields["mtu"] = value
		i.mtu = mtuVal

	case "speed":
		// Validate speed format (e.g., 10G, 25G, 40G, 100G)
		validSpeeds := map[string]bool{
			"1G": true, "10G": true, "25G": true, "40G": true, "50G": true, "100G": true, "200G": true, "400G": true,
		}
		if !validSpeeds[value] {
			return nil, fmt.Errorf("invalid speed: %s (valid: 1G, 10G, 25G, 40G, 50G, 100G, 200G, 400G)", value)
		}
		fields["speed"] = value
		i.speed = value

	case "admin-status", "admin_status":
		if value != "up" && value != "down" {
			return nil, fmt.Errorf("admin-status must be 'up' or 'down'")
		}
		fields["admin_status"] = value
		i.adminStatus = value

	case "description":
		fields["description"] = value

	default:
		return nil, fmt.Errorf("unknown property: %s (valid: mtu, speed, admin-status, description)", property)
	}

	// Determine which table to update based on interface type
	tableName := "PORT"
	if i.IsPortChannel() {
		tableName = "PORTCHANNEL"
	}

	cs.Add(tableName, i.name, ChangeModify, nil, fields)

	util.WithDevice(n.Name()).Infof("Set %s=%s on interface %s", property, value, i.name)
	return cs, nil
}

// ============================================================================
// DependencyChecker - checks reverse dependencies for safe deletion
// ============================================================================

// DependencyChecker checks if resources can be safely deleted.
// Used by Interface.RemoveService() to determine when shared resources
// (ACLs, VLANs, VRFs) can be deleted vs just having this interface removed.
// This is the single source of truth; pkg/operations re-exports it as a type alias.
type DependencyChecker struct {
	node           *Node
	excludeInterface string
}

// NewDependencyChecker creates a dependency checker for the given interface
func NewDependencyChecker(d *Node, excludeInterface string) *DependencyChecker {
	return &DependencyChecker{
		node:           d,
		excludeInterface: excludeInterface,
	}
}

// IsLastACLUser returns true if this is the last interface using the ACL
func (dc *DependencyChecker) IsLastACLUser(aclName string) bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return true
	}

	remaining := removeInterfaceFromList(acl.Ports, dc.excludeInterface)
	return remaining == ""
}

// GetACLRemainingInterfaces returns interfaces remaining after removing this one
func (dc *DependencyChecker) GetACLRemainingInterfaces(aclName string) string {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return ""
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return ""
	}

	return removeInterfaceFromList(acl.Ports, dc.excludeInterface)
}

// IsLastVLANMember returns true if this is the last member of the VLAN
func (dc *DependencyChecker) IsLastVLANMember(vlanID int) bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)
	count := 0
	for key := range configDB.VLANMember {
		if strings.HasPrefix(key, vlanName+"|") {
			memberPort := key[len(vlanName)+1:]
			if memberPort != dc.excludeInterface {
				count++
			}
		}
	}
	return count == 0
}

// IsLastServiceUser returns true if this is the last interface using the service
func (dc *DependencyChecker) IsLastServiceUser(serviceName string) bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}

	count := 0
	for intfName, binding := range configDB.NewtronServiceBinding {
		if binding.ServiceName == serviceName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastIPVPNUser returns true if this is the last interface bound to a service
// that references the given ipvpn name. Used for shared VRF cleanup — the VRF
// is only deleted when no service binding references the ipvpn.
func (dc *DependencyChecker) IsLastIPVPNUser(ipvpnName string) bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}

	count := 0
	for intfName, binding := range configDB.NewtronServiceBinding {
		if binding.IPVPN == ipvpnName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

