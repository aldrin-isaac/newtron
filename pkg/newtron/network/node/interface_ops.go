package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/util"
)

// InterfaceExists checks if an interface exists.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
// Three-way check: physical ports from RegisterPort map, PortChannels and VLAN SVIs from intents.
func (n *Node) InterfaceExists(name string) bool {
	name = util.NormalizeInterfaceName(name)
	// Physical ports: populated by RegisterPort
	if _, ok := n.interfaces[name]; ok {
		return true
	}
	// PortChannels: intent-managed
	if strings.HasPrefix(name, "PortChannel") && n.GetIntent("portchannel|"+name) != nil {
		return true
	}
	// VLAN SVIs: intent-managed
	if strings.HasPrefix(name, "Vlan") {
		vlanID := strings.TrimPrefix(name, "Vlan")
		if n.GetIntent("vlan|"+vlanID) != nil {
			return true
		}
	}
	return false
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
		entries = append(enableIpRoutingConfig(i.name), assignIpAddressConfig(i.name,ipAddr)...)
	} else {
		entries = assignIpAddressConfig(i.name,ipAddr)
	}
	cs := buildChangeSet(n.Name(), "interface.set-ip", entries, ChangeAdd)

	if err := n.render(cs); err != nil {
		return nil, err
	}
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
	cs.Deletes(deleteInterfaceIPConfig(i.name, ipAddr))

	// If no other IPs remain, remove the base INTERFACE entry too
	remaining := 0
	for _, addr := range i.IPAddresses() {
		if addr != ipAddr {
			remaining++
		}
	}
	if remaining == 0 {
		cs.Deletes(deleteInterfaceBaseConfig(i.name))
	}

	if err := n.render(cs); err != nil {
		return nil, err
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
	if vrfName != "" && vrfName != "default" && n.GetIntent("vrf|"+vrfName) == nil {
		return nil, fmt.Errorf("VRF '%s' does not exist", vrfName)
	}
	if i.IsPortChannelMember() {
		return nil, fmt.Errorf("cannot bind PortChannel member to VRF")
	}

	cs := buildChangeSet(n.Name(), "interface.set-vrf", bindVrfConfig(i.name,vrfName), ChangeModify)

	if err := n.render(cs); err != nil {
		return nil, err
	}
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
	parents := []string{"device"}
	if i.IsPortChannel() {
		parents = append(parents, "portchannel|"+i.name)
	}
	return i.node.writeIntent(cs, sonic.OpInterfaceInit, resource, map[string]string{}, parents)
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
		if n.GetIntent(fmt.Sprintf("vlan|%d", cfg.VLAN)) == nil {
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
	if i.IsPortChannel() {
		parents = append(parents, "portchannel|"+i.name)
	}
	if err := i.node.writeIntent(cs, sonic.OpConfigureInterface, "interface|"+i.name, configureIntentParams, parents); err != nil {
		return nil, err
	}

	// VRF binding first (creates the INTERFACE base entry with vrf_name)
	if cfg.VRF != "" {
		if cfg.VRF != "default" && n.GetIntent("vrf|"+cfg.VRF) == nil {
			return nil, fmt.Errorf("VRF '%s' does not exist", cfg.VRF)
		}
		cs.Adds(bindVrfConfig(i.name,cfg.VRF))
	}

	// IP address (requires base entry — either from VRF binding above or enableIpRouting)
	if cfg.IP != "" {
		if !util.IsValidIPv4CIDR(cfg.IP) {
			return nil, fmt.Errorf("invalid IP address: %s", cfg.IP)
		}
		if cfg.VRF == "" && i.VRF() == "" {
			// No VRF binding — need base INTERFACE entry for IP routing
			cs.Adds(enableIpRoutingConfig(i.name))
		}
		cs.Adds(assignIpAddressConfig(i.name,cfg.IP))
	}

	cs.ReverseOp = "interface.unconfigure-interface"
	cs.OperationParams = map[string]string{"interface": i.name}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Configured interface %s (vrf=%s, ip=%s, vlan=%d)", i.name, cfg.VRF, cfg.IP, cfg.VLAN)
	return cs, nil
}

// UnconfigureInterface is the reverse of ConfigureInterface. Performs a complete
// teardown: removes all sub-resources (BGP peer, QoS, ACL bindings, properties),
// then removes the interface role (VLAN membership or VRF/IP binding).
// Parameterless — the intent records are self-sufficient for teardown.
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

	// Remove all sub-resources (children before parent, per I5).
	// Snapshot the children list since removals mutate it.
	children := make([]string, len(intent.Children))
	copy(children, intent.Children)

	for _, childKey := range children {
		childIntent := n.GetIntent(childKey)
		if childIntent == nil {
			continue
		}
		switch childIntent.Operation {
		case sonic.OpAddBGPPeer:
			subCS, err := i.RemoveBGPPeer(ctx)
			if err != nil {
				return nil, fmt.Errorf("remove bgp-peer on %s: %w", i.name, err)
			}
			cs.Merge(subCS)

		case sonic.OpApplyQoS:
			subCS, err := i.RemoveQoS(ctx)
			if err != nil {
				return nil, fmt.Errorf("remove qos on %s: %w", i.name, err)
			}
			cs.Merge(subCS)

		case sonic.OpBindACL:
			aclName := childIntent.Params[sonic.FieldACLName]
			subCS, err := i.UnbindACL(ctx, aclName)
			if err != nil {
				return nil, fmt.Errorf("unbind acl %s on %s: %w", aclName, i.name, err)
			}
			cs.Merge(subCS)

		case sonic.OpSetProperty:
			// Properties are simple value overrides — just delete the intent.
			// The CONFIG_DB field persists as device reality.
			if err := n.deleteIntent(cs, childKey); err != nil {
				return nil, err
			}
		}
	}

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
		cs.Deletes(deleteInterfaceIPConfig(i.name, ip))
	}

	if vrf != "" {
		remaining := 0
		for _, addr := range i.IPAddresses() {
			if addr != ip {
				remaining++
			}
		}
		if remaining == 0 {
			cs.Deletes(deleteInterfaceBaseConfig(i.name))
		} else {
			cs.Adds(bindVrfConfig(i.name,""))
		}
	} else if ip != "" {
		remaining := 0
		for _, addr := range i.IPAddresses() {
			if addr != ip {
				remaining++
			}
		}
		if remaining == 0 {
			cs.Deletes(deleteInterfaceBaseConfig(i.name))
		}
	}

	if err := n.deleteIntent(cs, "interface|"+i.name); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
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
	if n.GetIntent("acl|"+aclName) == nil {
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

	// ACLs are shared — collect port list from intents (this interface's
	// binding intent was written above, so aclPortsFromIntents includes it)
	currentPorts := n.aclPortsFromIntents(aclName, direction)

	e := bindAclConfig(aclName, currentPorts, direction)
	cs.Update(e.Table, e.Key, e.Fields)
	if err := n.render(cs); err != nil {
		return nil, err
	}

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

	// Collect remaining ports from intents (this interface's intent hasn't been
	// deleted yet, so explicitly exclude it)
	allPorts := n.aclPortsFromIntents(aclName, direction)
	remainingPorts := util.RemoveFromCSV(allPorts, i.name)
	e := updateAclPorts(aclName, remainingPorts)
	cs.Update(e.Table, e.Key, e.Fields)

	if err := i.node.deleteIntent(cs, "interface|"+i.name+"|acl|"+direction); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
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

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpSetProperty)
	if err := i.ensureInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpSetProperty, "interface|"+i.name+"|"+property,
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

	cs.Updates(setPropertyConfig(tableName, i.name, fields))
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.Name()).Infof("Set %s=%s on interface %s", property, value, i.name)
	return cs, nil
}

// ClearProperty removes a property override from this interface, reverting
// the field to its default. Deletes the property intent so it no longer
// blocks parent intent deletion.
func (i *Interface) ClearProperty(ctx context.Context, property string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("clear-property", i.name).Result(); err != nil {
		return nil, err
	}

	intentKey := "interface|" + i.name + "|" + property
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, fmt.Errorf("no property intent for %s on %s", property, i.name)
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpClearProperty)

	tableName := "PORT"
	if i.IsPortChannel() {
		tableName = "PORTCHANNEL"
	}

	switch property {
	case "mtu", "speed", "admin-status", "admin_status", "description":
		cs.Updates(clearPropertyConfig(tableName, i.name, property))
	default:
		return nil, fmt.Errorf("unknown property: %s", property)
	}

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.Name()).Infof("Cleared %s on interface %s", property, i.name)
	return cs, nil
}


