package node

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// Interface represents a network interface within the context of a Device.
//
// Key design: Interface has a parent reference to Node, which in turn has
// a parent reference to Network. This provides hierarchical access:
//
//	intf.Node()                          // Get parent Node
//	intf.Node().Network()                // Get Network from Node
//	intf.Node().Network().GetService()   // Access Network-level config
//	intf.Node().ASNumber()               // Access Device-level config
//
// All state is read on demand from the Node's ConfigDB/StateDB snapshots
// (refreshed at Lock time), ensuring callers always see the latest data
// within a write episode.
//
// Hierarchy: Network -> Device -> Interface
type Interface struct {
	// Parent reference - provides access to Device and Network configuration
	node *Node

	// Interface identity
	name string
}

// ============================================================================
// Parent Accessors - Key to OO Design
// ============================================================================

// Device returns the parent Device object.
// This allows access to Device-level properties and Network configuration.
//
// Example usage:
//
//	asNum := intf.Node().ASNumber()
//	neighbors := intf.Node().BGPNeighbors()
func (i *Interface) Node() *Node {
	return i.node
}


// ============================================================================
// Interface Properties (on-demand from ConfigDB/StateDB)
// ============================================================================

// Name returns the interface name (e.g., "Ethernet0", "PortChannel100").
func (i *Interface) Name() string {
	return i.name
}

// AdminStatus returns the administrative status (up/down).
// SetProperty overrides are checked first, then pre-intent defaults.
func (i *Interface) AdminStatus() string {
	// Check for SetProperty override (works for both physical and PortChannel)
	if propIntent := i.node.GetIntent("interface|" + i.name + "|admin_status"); propIntent != nil {
		return propIntent.Params[sonic.FieldValue]
	}
	// PortChannel default from creation
	if i.IsPortChannel() {
		return "up"
	}
	// Physical port from PORT table (pre-intent infrastructure)
	if configDB := i.node.ConfigDB(); configDB != nil {
		if port, ok := configDB.Port[i.name]; ok {
			return port.AdminStatus
		}
	}
	return ""
}

// OperStatus returns the operational status (up/down).
func (i *Interface) OperStatus() string {
	stateDB := i.node.StateDB()
	if stateDB == nil {
		return ""
	}
	if ps, ok := stateDB.PortTable[i.name]; ok {
		return ps.OperStatus
	}
	if ls, ok := stateDB.LAGTable[i.name]; ok {
		return ls.OperStatus
	}
	return ""
}

// Speed returns the interface speed.
// Prefers operational value from StateDB; falls back to ConfigDB.
func (i *Interface) Speed() string {
	if stateDB := i.node.StateDB(); stateDB != nil {
		if ps, ok := stateDB.PortTable[i.name]; ok && ps.Speed != "" {
			return ps.Speed
		}
	}
	if configDB := i.node.ConfigDB(); configDB != nil {
		if port, ok := configDB.Port[i.name]; ok {
			return port.Speed
		}
	}
	return ""
}

// MTU returns the interface MTU.
// Prefers operational value from StateDB; falls back to intent/ConfigDB.
func (i *Interface) MTU() int {
	if stateDB := i.node.StateDB(); stateDB != nil {
		if ps, ok := stateDB.PortTable[i.name]; ok && ps.MTU != "" {
			mtu, _ := strconv.Atoi(ps.MTU)
			return mtu
		}
	}
	// Check for SetProperty override
	if propIntent := i.node.GetIntent("interface|" + i.name + "|mtu"); propIntent != nil {
		mtu, _ := strconv.Atoi(propIntent.Params[sonic.FieldValue])
		return mtu
	}
	// PortChannel creation intent stores MTU
	if i.IsPortChannel() {
		if intent := i.node.GetIntent("portchannel|" + i.name); intent != nil {
			if mtuStr := intent.Params["mtu"]; mtuStr != "" {
				mtu, _ := strconv.Atoi(mtuStr)
				return mtu
			}
		}
		return 0
	}
	// Physical port from PORT table (pre-intent infrastructure)
	if configDB := i.node.ConfigDB(); configDB != nil {
		if port, ok := configDB.Port[i.name]; ok && port.MTU != "" {
			mtu, _ := strconv.Atoi(port.MTU)
			return mtu
		}
	}
	return 0
}

// VRF returns the VRF this interface is bound to.
func (i *Interface) VRF() string {
	intent := i.node.GetIntent("interface|" + i.name)
	if intent == nil {
		return ""
	}
	return intent.Params[sonic.FieldVRF]
}

// IPAddresses returns the IP addresses configured on this interface.
func (i *Interface) IPAddresses() []string {
	intent := i.node.GetIntent("interface|" + i.name)
	if intent == nil {
		return nil
	}
	// configure-interface stores IP in FieldIntfIP ("ip") param
	if ip := intent.Params[sonic.FieldIntfIP]; ip != "" {
		return []string{ip}
	}
	// configure-irb stores IP in FieldIPAddress ("ip_address") param
	if ip := intent.Params[sonic.FieldIPAddress]; ip != "" {
		return []string{ip}
	}
	return nil
}

// ============================================================================
// Intent (on-demand from NEWTRON_INTENT)
// ============================================================================

// binding returns the raw intent entry from CONFIG_DB.
// Used internally by RemoveService to read all binding fields at once.
func (i *Interface) binding() map[string]string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return map[string]string{}
	}
	if entry, ok := configDB.NewtronIntent["interface|"+i.name]; ok {
		return entry
	}
	return map[string]string{}
}

// ServiceName returns the name of the service bound to this interface.
func (i *Interface) ServiceName() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if entry, ok := configDB.NewtronIntent["interface|"+i.name]; ok {
		return entry["service_name"]
	}
	return ""
}

// HasService returns true if a service is bound to this interface.
func (i *Interface) HasService() bool {
	return i.ServiceName() != ""
}

// IngressACL returns the name of the ingress ACL bound to this interface.
// Checks service binding intent first, then standalone BindACL intent.
func (i *Interface) IngressACL() string {
	// Service binding intent: interface|{name} → ingress_acl param
	if intent := i.node.GetIntent("interface|" + i.name); intent != nil {
		if aclName := intent.Params["ingress_acl"]; aclName != "" {
			return aclName
		}
	}
	// Standalone BindACL intent: interface|{name}|acl|ingress → acl_name param
	if intent := i.node.GetIntent("interface|" + i.name + "|acl|ingress"); intent != nil {
		return intent.Params[sonic.FieldACLName]
	}
	return ""
}

// EgressACL returns the name of the egress ACL bound to this interface.
// Checks service binding intent first, then standalone BindACL intent.
func (i *Interface) EgressACL() string {
	// Service binding intent: interface|{name} → egress_acl param
	if intent := i.node.GetIntent("interface|" + i.name); intent != nil {
		if aclName := intent.Params["egress_acl"]; aclName != "" {
			return aclName
		}
	}
	// Standalone BindACL intent: interface|{name}|acl|egress → acl_name param
	if intent := i.node.GetIntent("interface|" + i.name + "|acl|egress"); intent != nil {
		return intent.Params[sonic.FieldACLName]
	}
	return ""
}

// ============================================================================
// PortChannel Membership (on-demand from PORTCHANNEL_MEMBER)
// ============================================================================

// IsPortChannelMember returns true if this interface is a PortChannel member.
func (i *Interface) IsPortChannelMember() bool {
	return i.PortChannelParent() != ""
}

// PortChannelParent returns the name of the parent PortChannel (if this is a member).
func (i *Interface) PortChannelParent() string {
	for resource := range i.node.IntentsByPrefix("portchannel|") {
		// Member intents have the form: portchannel|PortChannel100|Ethernet0
		parts := strings.SplitN(resource, "|", 3)
		if len(parts) == 3 && parts[2] == i.name {
			return parts[1]
		}
	}
	return ""
}

// Description returns the interface description.
// Checks SetProperty intent first, then PORT table (pre-intent infrastructure).
func (i *Interface) Description() string {
	// Check for SetProperty override
	if propIntent := i.node.GetIntent("interface|" + i.name + "|description"); propIntent != nil {
		return propIntent.Params[sonic.FieldValue]
	}
	// Physical port from PORT table (pre-intent infrastructure)
	if configDB := i.node.ConfigDB(); configDB != nil {
		if port, ok := configDB.Port[i.name]; ok {
			return port.Description
		}
	}
	return ""
}

// PortChannelMembers returns the member interfaces if this is a PortChannel.
func (i *Interface) PortChannelMembers() []string {
	if !i.IsPortChannel() {
		return nil
	}
	memberIntents := i.node.IntentsByPrefix("portchannel|" + i.name + "|")
	var members []string
	for _, intent := range memberIntents {
		if memberName := intent.Params[sonic.FieldName]; memberName != "" {
			members = append(members, memberName)
		}
	}
	return members
}

// VLANMembers returns the member interfaces if this is a VLAN interface.
func (i *Interface) VLANMembers() []string {
	if !i.IsVLAN() {
		return nil
	}
	// Extract VLAN ID from name (e.g., "Vlan100" → "100")
	vlanID := strings.TrimPrefix(i.name, "Vlan")

	var members []string
	for resource := range i.node.IntentsByParam(sonic.FieldVLANID, vlanID) {
		// Only interface intents (not vlan|, macvpn|, etc.)
		if !strings.HasPrefix(resource, "interface|") {
			continue
		}
		intfName := strings.TrimPrefix(resource, "interface|")
		// Skip IRB intents (interface|Vlan*)
		if strings.HasPrefix(intfName, "Vlan") {
			continue
		}
		members = append(members, intfName)
	}
	return members
}

// BGPNeighbors returns BGP neighbors configured on this interface.
func (i *Interface) BGPNeighbors() []string {
	// Underlay peers: interface|{name}|bgp-peer intents
	peerIntents := i.node.IntentsByPrefix("interface|" + i.name + "|bgp-peer")
	var neighbors []string
	for _, intent := range peerIntents {
		if ip := intent.Params["neighbor_ip"]; ip != "" {
			neighbors = append(neighbors, ip)
		}
	}
	return neighbors
}

// ============================================================================
// Interface Type Detection
// ============================================================================

// IsPortChannel returns true if this is a port channel (PortChannel*).
func (i *Interface) IsPortChannel() bool {
	return strings.HasPrefix(i.name, "PortChannel")
}

// IsVLAN returns true if this is a VLAN interface (Vlan*).
func (i *Interface) IsVLAN() bool {
	return strings.HasPrefix(i.name, "Vlan")
}

// ============================================================================
// String Representation
// ============================================================================

// String returns a string representation of the interface.
func (i *Interface) String() string {
	adminStatus := i.AdminStatus()
	operStatus := i.OperStatus()

	status := "down"
	if adminStatus == "up" && operStatus == "up" {
		status = "up"
	} else if adminStatus == "up" {
		status = "admin-up/oper-down"
	}

	desc := fmt.Sprintf("%s (%s)", i.name, status)

	if svcName := i.ServiceName(); svcName != "" {
		desc += fmt.Sprintf(" [service: %s]", svcName)
	}
	if ipAddrs := i.IPAddresses(); len(ipAddrs) > 0 {
		desc += fmt.Sprintf(" [ip: %s]", strings.Join(ipAddrs, ", "))
	}
	if vrf := i.VRF(); vrf != "" {
		desc += fmt.Sprintf(" [vrf: %s]", vrf)
	}
	if pcParent := i.PortChannelParent(); pcParent != "" {
		desc += fmt.Sprintf(" [member of: %s]", pcParent)
	}

	return desc
}
