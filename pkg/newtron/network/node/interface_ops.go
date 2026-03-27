package node

import (
	"context"
	"fmt"
	"strconv"
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
// Routed mode (VRF+IP) and bridged mode (VLAN) are mutually exclusive.
type InterfaceConfig struct {
	VRF    string // VRF binding (routed mode)
	IP     string // IP address in CIDR notation (routed mode)
	VLAN   int    // VLAN ID (bridged mode)
	Tagged bool   // Tagged membership (bridged mode)
}

// ensureInterfaceIntent lazily creates the interface|INTF intent if it doesn't
// exist. Sub-resource operations (SetProperty, BindACL, ApplyQoS) call this so
// they work on interfaces that haven't had ConfigureInterface called.
func (i *Interface) ensureInterfaceIntent(cs *ChangeSet) error {
	resource := "interface|" + i.name
	if i.node.GetIntent(resource) != nil {
		return nil
	}
	return i.node.writeIntent(cs, sonic.OpInterfaceInit, resource, map[string]string{}, []string{"device"})
}

// ConfigureInterface sets forwarding mode on an interface. Routed mode (VRF+IP)
// and bridged mode (VLAN membership) are mutually exclusive. This is the
// intent-producing method that topology steps should use.
func (i *Interface) ConfigureInterface(ctx context.Context, cfg InterfaceConfig) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition(sonic.OpConfigureInterface, i.name).Result(); err != nil {
		return nil, err
	}
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot configure PortChannel member directly")
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpConfigureInterface)
	configureIntentParams := map[string]string{}

	// Bridged mode — VLAN membership
	if cfg.VLAN > 0 {
		if cfg.VRF != "" || cfg.IP != "" {
			return nil, fmt.Errorf("cannot mix routed (VRF/IP) and bridged (VLAN) config")
		}
		if !n.VLANExists(cfg.VLAN) {
			return nil, fmt.Errorf("VLAN %d does not exist", cfg.VLAN)
		}
		cs.Adds(createVlanMemberConfig(cfg.VLAN, i.name, cfg.Tagged))
		configureIntentParams[sonic.FieldVLANID] = strconv.Itoa(cfg.VLAN)
		configureIntentParams[sonic.FieldTagged] = strconv.FormatBool(cfg.Tagged)
	}

	// Routed mode — VRF binding and/or IP address
	if cfg.VRF != "" {
		configureIntentParams[sonic.FieldVRF] = cfg.VRF
	}
	if cfg.IP != "" {
		configureIntentParams[sonic.FieldIntfIP] = cfg.IP
	}

	var parents []string
	if cfg.VLAN > 0 {
		parents = []string{"vlan|" + strconv.Itoa(cfg.VLAN)}
	} else if cfg.VRF != "" {
		parents = []string{"vrf|" + cfg.VRF}
	} else {
		parents = []string{"device"}
	}
	if err := i.node.writeIntent(cs, sonic.OpConfigureInterface, "interface|"+i.name, configureIntentParams, parents); err != nil {
		return nil, err
	}

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

	cs.ReverseOp = "interface.unconfigure-interface"
	cs.OperationParams = map[string]string{"interface": i.name}
	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Configured interface %s (vrf=%s, ip=%s, vlan=%d)", i.name, cfg.VRF, cfg.IP, cfg.VLAN)
	return cs, nil
}

// UnconfigureInterface is the reverse of ConfigureInterface. It reads the intent
// record to determine what was configured, then undoes it. Parameterless — the
// intent record is self-sufficient for teardown.
func (i *Interface) UnconfigureInterface(ctx context.Context) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unconfigure-interface", i.name).Result(); err != nil {
		return nil, err
	}

	intent := n.GetIntent("interface|" + i.name)
	if intent == nil {
		return nil, fmt.Errorf("no configuration intent for %s", i.name)
	}

	cs := NewChangeSet(n.Name(), "interface.unconfigure-interface")

	// Bridged mode — remove VLAN membership
	if vlanStr := intent.Params[sonic.FieldVLANID]; vlanStr != "" {
		vlanID, _ := strconv.Atoi(vlanStr)
		if vlanID > 0 {
			cs.Deletes(deleteVlanMemberConfig(vlanID, i.name))
		}
	}

	// Routed mode — remove IP then VRF
	ip := intent.Params[sonic.FieldIntfIP]
	vrf := intent.Params[sonic.FieldVRF]

	if ip != "" {
		cs.Delete("INTERFACE", fmt.Sprintf("%s|%s", i.name, ip))
	}

	if vrf != "" {
		remaining := 0
		for _, addr := range i.IPAddresses() {
			if addr != ip {
				remaining++
			}
		}
		if remaining == 0 {
			cs.Delete("INTERFACE", i.name)
		} else {
			cs.Adds(i.bindVrf(""))
		}
	} else if ip != "" {
		remaining := 0
		for _, addr := range i.IPAddresses() {
			if addr != ip {
				remaining++
			}
		}
		if remaining == 0 {
			cs.Delete("INTERFACE", i.name)
		}
	}

	if err := n.deleteIntent(cs, "interface|"+i.name); err != nil {
		return nil, err
	}
	n.applyShadow(cs)
	util.WithDevice(n.Name()).Infof("Unconfigured interface %s", i.name)
	return cs, nil
}

// BindACL binds an ACL to this interface.
// ACLs are shared - adds this interface to the ACL's binding list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition(sonic.OpBindACL, i.name).Result(); err != nil {
		return nil, err
	}
	if !n.ACLTableExists(aclName) {
		return nil, fmt.Errorf("ACL table '%s' does not exist", aclName)
	}
	if direction != "ingress" && direction != "egress" {
		return nil, fmt.Errorf("direction must be 'ingress' or 'egress'")
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpBindACL)
	if err := i.ensureInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpBindACL, "interface|"+i.name+"|acl|"+direction,
		map[string]string{sonic.FieldACLName: aclName, sonic.FieldDirection: direction},
		[]string{"interface|" + i.name, "acl|" + aclName}); err != nil {
		return nil, err
	}
	cs.ReverseOp = "interface.unbind-acl"
	cs.OperationParams = map[string]string{"interface": i.name, "acl_name": aclName}

	// ACLs are shared — add this interface to existing binding list
	configDB := n.ConfigDB()
	newBindings := util.AddToCSV(configDB.ACLTable[aclName].Ports, i.name)

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

	// Find the intent record for this ACL binding. Try both directions since
	// the caller passes aclName but not direction.
	var direction string
	for _, dir := range []string{"ingress", "egress"} {
		if intent := n.GetIntent("interface|" + i.name + "|acl|" + dir); intent != nil {
			if intent.Params[sonic.FieldACLName] == aclName {
				direction = dir
				break
			}
		}
	}
	if direction == "" {
		return nil, fmt.Errorf("no ACL binding intent for %s on %s", aclName, i.name)
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-acl")

	// Remove this interface from the ACL's ports list (shared resource — must read current state)
	configDB := n.ConfigDB()
	if configDB != nil {
		if table, ok := configDB.ACLTable[aclName]; ok {
			e := updateAclPorts(aclName, util.RemoveFromCSV(table.Ports, i.name))
			cs.Update(e.Table, e.Key, e.Fields)
		}
	}

	if err := i.node.deleteIntent(cs, "interface|"+i.name+"|acl|"+direction); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Unbound ACL %s from interface %s", aclName, i.name)
	return cs, nil
}

// ============================================================================
// Generic Property Setting
// ============================================================================

// SetProperty sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description
func (i *Interface) SetProperty(ctx context.Context, property, value string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("set-property", i.name).Result(); err != nil {
		return nil, err
	}
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot configure PortChannel member directly - configure the parent PortChannel")
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpSetPortProperty)
	if err := i.ensureInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpSetPortProperty, "interface|"+i.name+"|"+property,
		map[string]string{sonic.FieldProperty: property, sonic.FieldValue: value},
		[]string{"interface|" + i.name}); err != nil {
		return nil, err
	}
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



