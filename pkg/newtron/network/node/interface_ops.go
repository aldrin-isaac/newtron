package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// InterfaceExists checks if an interface exists.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
func (n *Node) InterfaceExists(name string) bool {
	return n.configDB.HasInterface(util.NormalizeInterfaceName(name))
}

// interfaceBaseConfig returns the base INTERFACE entry with optional fields.
// This is the single-owner function for creating INTERFACE table base entries.
func interfaceBaseConfig(intfName string, fields map[string]string) []sonic.Entry {
	if fields == nil {
		fields = map[string]string{}
	}
	return []sonic.Entry{
		{Table: "INTERFACE", Key: intfName, Fields: fields},
	}
}

// interfaceIPSubEntry returns the INTERFACE IP sub-entry (e.g., "Ethernet0|10.1.1.1/30").
func interfaceIPSubEntry(intfName, ipAddr string) sonic.Entry {
	return sonic.Entry{
		Table:  "INTERFACE",
		Key:    fmt.Sprintf("%s|%s", intfName, ipAddr),
		Fields: map[string]string{},
	}
}

// interfaceIPConfig returns sonic.Entry for configuring an IP on an interface.
// Creates the INTERFACE base entry + IP sub-entry.
func interfaceIPConfig(intfName, ipAddr string) []sonic.Entry {
	return append(interfaceBaseConfig(intfName, nil), interfaceIPSubEntry(intfName, ipAddr))
}

// ============================================================================
// Interface Property Operations
// ============================================================================

// SetIP configures an IP address on this interface.
func (i *Interface) SetIP(ctx context.Context, ipAddr string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("set-ip", i.name).Result(); err != nil {
		return nil, err
	}
	if !util.IsValidIPv4CIDR(ipAddr) {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddr)
	}
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot configure IP on PortChannel member")
	}

	cs := NewChangeSet(n.Name(), "interface.set-ip")
	// SONiC requires both the base interface entry and the IP entry.
	// The base entry enables L3 on the interface; the IP entry assigns the address.
	cs.Add("INTERFACE", i.name, ChangeAdd, map[string]string{})
	ipKey := fmt.Sprintf("%s|%s", i.name, ipAddr)
	cs.Add("INTERFACE", ipKey, ChangeAdd, map[string]string{})

	n.trackOffline(cs)
	util.WithDevice(n.Name()).Infof("Configured IP %s on interface %s", ipAddr, i.name)
	return cs, nil
}

// RemoveIP removes an IP address from this interface.
func (i *Interface) RemoveIP(ctx context.Context, ipAddr string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("remove-ip", i.name).Result(); err != nil {
		return nil, err
	}
	if !util.IsValidIPv4CIDR(ipAddr) {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddr)
	}

	cs := NewChangeSet(n.Name(), "interface.remove-ip")
	ipKey := fmt.Sprintf("%s|%s", i.name, ipAddr)
	cs.Add("INTERFACE", ipKey, ChangeDelete, nil)

	// If no other IPs remain, remove the base INTERFACE entry too
	remaining := 0
	for _, addr := range i.IPAddresses() {
		if addr != ipAddr {
			remaining++
		}
	}
	if remaining == 0 {
		cs.Add("INTERFACE", i.name, ChangeDelete, nil)
	}

	util.WithDevice(n.Name()).Infof("Removed IP %s from interface %s", ipAddr, i.name)
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
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot bind PortChannel member to VRF")
	}

	cs := NewChangeSet(n.Name(), "interface.set-vrf")
	cs.Add("INTERFACE", i.name, ChangeModify, map[string]string{
		"vrf_name": vrfName,
	})

	n.trackOffline(cs)
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
	dc := NewDependencyChecker(n, "")
	var newBindings string
	if dc.IsFirstACLUser(aclName) {
		newBindings = i.name
	} else {
		newBindings = addInterfaceToList(configDB.ACLTable[aclName].Ports, i.name)
	}

	cs.Add("ACL_TABLE", aclName, ChangeModify, map[string]string{
		"ports": newBindings,
		"stage": direction,
	})

	util.WithDevice(n.Name()).Infof("Bound ACL %s to interface %s (%s)", aclName, i.name, direction)
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
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot configure PortChannel member directly - configure the parent PortChannel")
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

	case "speed":
		// Validate speed format (e.g., 10G, 25G, 40G, 100G)
		validSpeeds := map[string]bool{
			"1G": true, "10G": true, "25G": true, "40G": true, "50G": true, "100G": true, "200G": true, "400G": true,
		}
		if !validSpeeds[value] {
			return nil, fmt.Errorf("invalid speed: %s (valid: 1G, 10G, 25G, 40G, 50G, 100G, 200G, 400G)", value)
		}
		fields["speed"] = value

	case "admin-status", "admin_status":
		if value != "up" && value != "down" {
			return nil, fmt.Errorf("admin-status must be 'up' or 'down'")
		}
		fields["admin_status"] = value

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

	cs.Add(tableName, i.name, ChangeModify, fields)

	util.WithDevice(n.Name()).Infof("Set %s=%s on interface %s", property, value, i.name)
	return cs, nil
}

// ============================================================================
// DependencyChecker - first/last membership checks for shared resources
// ============================================================================

// DependencyChecker answers "is this the first or last user of a shared resource?"
// from the perspective of a specific interface.
//
// IsFirst* methods: the interface is about to be added — it is not yet in the
// collection, so excludeInterface is not used. These check current state only.
//
// IsLast* methods: the interface is about to be removed — it is counted as
// absent, so remaining users = (total − excludeInterface). These determine
// whether shared resources (ACLs, VLANs, VRFs) can be safely deleted.
//
// Used by Interface.RemoveService() and Interface.BindACL().
type DependencyChecker struct {
	node             *Node
	excludeInterface string // used only by IsLast* methods
}

// NewDependencyChecker creates a dependency checker for the given interface
func NewDependencyChecker(d *Node, excludeInterface string) *DependencyChecker {
	return &DependencyChecker{
		node:           d,
		excludeInterface: excludeInterface,
	}
}

// IsFirstACLUser returns true if no interface is currently bound to the ACL.
// excludeInterface is not used — the caller has not yet been added.
func (dc *DependencyChecker) IsFirstACLUser(aclName string) bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}
	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return true
	}
	return acl.Ports == ""
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

