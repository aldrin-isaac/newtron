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

// bindVrf returns the INTERFACE entry for binding this interface to a VRF.
// Always includes the vrf_name field: pass "" to clear the VRF binding.
func (i *Interface) bindVrf(vrfName string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: i.name,
		Fields: map[string]string{"vrf_name": vrfName}}}
}

// enableIpRouting returns the base INTERFACE entry that enables IP routing on this interface.
// No VRF binding — just empty fields so SONiC intfmgrd creates the routing entry.
func (i *Interface) enableIpRouting() []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE", Key: i.name,
		Fields: map[string]string{}}}
}

// assignIpAddress returns the INTERFACE entry for assigning an IP address.
func (i *Interface) assignIpAddress(ipAddr string) []sonic.Entry {
	return []sonic.Entry{{Table: "INTERFACE",
		Key: fmt.Sprintf("%s|%s", i.name, ipAddr), Fields: map[string]string{}}}
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

	// SONiC requires both the base interface entry and the IP entry.
	// The base entry enables routing on the interface; the IP entry assigns the address.
	// However, if the interface already has a VRF binding (INTERFACE|name with
	// vrf_name set), the base entry already exists — re-writing it with NULL:NULL
	// disrupts intfmgrd on CiscoVS (see RCA-037). Skip enableIpRouting in that case.
	var entries []sonic.Entry
	if i.VRF() == "" {
		entries = append(i.enableIpRouting(), i.assignIpAddress(ipAddr)...)
	} else {
		entries = i.assignIpAddress(ipAddr)
	}
	cs := buildChangeSet(n.Name(), "interface.set-ip", entries, ChangeAdd)

	n.applyShadow(cs)
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
	cs.Delete("INTERFACE", ipKey)

	// If no other IPs remain, remove the base INTERFACE entry too
	remaining := 0
	for _, addr := range i.IPAddresses() {
		if addr != ipAddr {
			remaining++
		}
	}
	if remaining == 0 {
		cs.Delete("INTERFACE", i.name)
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

	cs := buildChangeSet(n.Name(), "interface.set-vrf", i.bindVrf(vrfName), ChangeModify)

	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Bound interface %s to VRF %s", i.name, vrfName)
	return cs, nil
}

// InterfaceConfig holds the combined configuration for ConfigureInterface.
type InterfaceConfig struct {
	VRF string // VRF binding (empty = no VRF change)
	IP  string // IP address in CIDR notation (empty = no IP change)
}

// ConfigureInterface is a combined operation that sets VRF binding and IP address
// on an interface in a single ChangeSet. This is the intent-producing method that
// topology steps should use instead of separate SetIP/SetVRF calls.
func (i *Interface) ConfigureInterface(ctx context.Context, cfg InterfaceConfig) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("configure-interface", i.name).Result(); err != nil {
		return nil, err
	}
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot configure PortChannel member directly")
	}

	cs := NewChangeSet(n.Name(), "interface.configure-interface")

	// VRF binding first (creates the INTERFACE base entry with vrf_name)
	if cfg.VRF != "" {
		if cfg.VRF != "default" && !n.VRFExists(cfg.VRF) {
			return nil, fmt.Errorf("VRF '%s' does not exist", cfg.VRF)
		}
		cs.Adds(i.bindVrf(cfg.VRF))
	}

	// IP address (requires base entry — either from VRF binding above or enableIpRouting)
	if cfg.IP != "" {
		if !util.IsValidIPv4CIDR(cfg.IP) {
			return nil, fmt.Errorf("invalid IP address: %s", cfg.IP)
		}
		if cfg.VRF == "" && i.VRF() == "" {
			// No VRF binding — need base INTERFACE entry for IP routing
			cs.Adds(i.enableIpRouting())
		}
		cs.Adds(i.assignIpAddress(cfg.IP))
	}

	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Configured interface %s (vrf=%s, ip=%s)", i.name, cfg.VRF, cfg.IP)
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
	cs.ReverseOp = "interface.unbind-acl"
	cs.OperationParams = map[string]string{"interface": i.name, "acl_name": aclName}

	// ACLs are shared - add this interface to existing binding list
	configDB := n.ConfigDB()
	dc := NewDependencyChecker(n, "")
	var newBindings string
	if dc.IsFirstACLUser(aclName) {
		newBindings = i.name
	} else {
		newBindings = util.AddToCSV(configDB.ACLTable[aclName].Ports, i.name)
	}

	e := bindAclConfig(aclName, newBindings, direction)
	cs.Update(e.Table, e.Key, e.Fields)

	util.WithDevice(n.Name()).Infof("Bound ACL %s to interface %s (%s)", aclName, i.name, direction)
	return cs, nil
}

// UnbindACL removes this interface from an ACL table's binding list.
func (i *Interface) UnbindACL(ctx context.Context, aclName string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-acl", aclName).
		RequireACLTableExists(aclName).
		Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-acl")

	configDB := n.ConfigDB()
	if configDB != nil {
		if table, ok := configDB.ACLTable[aclName]; ok {
			e := updateAclPorts(aclName, util.RemoveFromCSV(table.Ports, i.name))
			cs.Update(e.Table, e.Key, e.Fields)
		}
	}

	util.WithDevice(n.Name()).Infof("Unbound ACL %s from interface %s", aclName, i.name)
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

	cs := NewChangeSet(n.Name(), "interface.set-port-property")
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

	cs.Update(tableName, i.name, fields)

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

	remaining := util.RemoveFromCSV(acl.Ports, dc.excludeInterface)
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

	return util.RemoveFromCSV(acl.Ports, dc.excludeInterface)
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
	for intfName, fields := range configDB.NewtronIntent {
		if fields["service_name"] == serviceName && intfName != dc.excludeInterface {
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
	for intfName, fields := range configDB.NewtronIntent {
		if fields["ipvpn"] == ipvpnName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastAnycastMACUser returns true if no other service binding references a
// macvpn with AnycastMAC set. Used to gate SAG_GLOBAL cleanup.
func (dc *DependencyChecker) IsLastAnycastMACUser() bool {
	configDB := dc.node.ConfigDB()
	if configDB == nil {
		return true
	}
	for intfName, fields := range configDB.NewtronIntent {
		if intfName == dc.excludeInterface {
			continue
		}
		if fields["anycast_mac"] != "" {
			return false
		}
	}
	return true
}

