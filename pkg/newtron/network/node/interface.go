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
func (i *Interface) AdminStatus() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if port, ok := configDB.Port[i.name]; ok {
		return port.AdminStatus
	}
	if pc, ok := configDB.PortChannel[i.name]; ok {
		return pc.AdminStatus
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
// Prefers operational value from StateDB; falls back to ConfigDB.
func (i *Interface) MTU() int {
	if stateDB := i.node.StateDB(); stateDB != nil {
		if ps, ok := stateDB.PortTable[i.name]; ok && ps.MTU != "" {
			mtu, _ := strconv.Atoi(ps.MTU)
			return mtu
		}
	}
	if configDB := i.node.ConfigDB(); configDB != nil {
		if port, ok := configDB.Port[i.name]; ok && port.MTU != "" {
			mtu, _ := strconv.Atoi(port.MTU)
			return mtu
		}
		if pc, ok := configDB.PortChannel[i.name]; ok && pc.MTU != "" {
			mtu, _ := strconv.Atoi(pc.MTU)
			return mtu
		}
	}
	return 0
}

// VRF returns the VRF this interface is bound to.
func (i *Interface) VRF() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if intf, ok := configDB.Interface[i.name]; ok {
		return intf.VRFName
	}
	return ""
}

// IPAddresses returns the IP addresses configured on this interface.
func (i *Interface) IPAddresses() []string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return nil
	}
	var addrs []string
	prefix := i.name + "|"
	for key := range configDB.Interface {
		if strings.HasPrefix(key, prefix) {
			addrs = append(addrs, strings.TrimPrefix(key, prefix))
		}
	}
	return addrs
}

// ============================================================================
// Service Binding (on-demand from NEWTRON_SERVICE_BINDING)
// ============================================================================

// binding returns the raw service binding entry from CONFIG_DB.
// Used internally by RemoveService to read all binding fields at once.
func (i *Interface) binding() sonic.ServiceBindingEntry {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return sonic.ServiceBindingEntry{}
	}
	return configDB.NewtronServiceBinding[i.name]
}

// ServiceName returns the name of the service bound to this interface.
func (i *Interface) ServiceName() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if binding, ok := configDB.NewtronServiceBinding[i.name]; ok {
		return binding.ServiceName
	}
	return ""
}

// HasService returns true if a service is bound to this interface.
func (i *Interface) HasService() bool {
	return i.ServiceName() != ""
}

// IngressACL returns the name of the ingress ACL bound to this interface.
// Prefers the service binding record; falls back to scanning ACL_TABLE.
func (i *Interface) IngressACL() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if binding, ok := configDB.NewtronServiceBinding[i.name]; ok && binding.IngressACL != "" {
		return binding.IngressACL
	}
	for aclName, acl := range configDB.ACLTable {
		if acl.Stage == "ingress" {
			for _, port := range strings.Split(acl.Ports, ",") {
				if strings.TrimSpace(port) == i.name {
					return aclName
				}
			}
		}
	}
	return ""
}

// EgressACL returns the name of the egress ACL bound to this interface.
// Prefers the service binding record; falls back to scanning ACL_TABLE.
func (i *Interface) EgressACL() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if binding, ok := configDB.NewtronServiceBinding[i.name]; ok && binding.EgressACL != "" {
		return binding.EgressACL
	}
	for aclName, acl := range configDB.ACLTable {
		if acl.Stage == "egress" {
			for _, port := range strings.Split(acl.Ports, ",") {
				if strings.TrimSpace(port) == i.name {
					return aclName
				}
			}
		}
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
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	for key := range configDB.PortChannelMember {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 && parts[1] == i.name {
			return parts[0]
		}
	}
	return ""
}

// Description returns the interface description (from PORT table).
func (i *Interface) Description() string {
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return ""
	}
	if port, ok := configDB.Port[i.name]; ok {
		return port.Description
	}
	if pc, ok := configDB.PortChannel[i.name]; ok {
		return pc.Description
	}
	return ""
}

// PortChannelMembers returns the member interfaces if this is a PortChannel.
func (i *Interface) PortChannelMembers() []string {
	if !i.IsPortChannel() {
		return nil
	}
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return nil
	}
	var members []string
	prefix := i.name + "|"
	for key := range configDB.PortChannelMember {
		if strings.HasPrefix(key, prefix) {
			member := strings.TrimPrefix(key, prefix)
			members = append(members, member)
		}
	}
	return members
}

// VLANMembers returns the member interfaces if this is a VLAN interface.
func (i *Interface) VLANMembers() []string {
	if !i.IsVLAN() {
		return nil
	}
	configDB := i.node.ConfigDB()
	if configDB == nil {
		return nil
	}
	var members []string
	prefix := i.name + "|"
	for key := range configDB.VLANMember {
		if strings.HasPrefix(key, prefix) {
			member := strings.TrimPrefix(key, prefix)
			members = append(members, member)
		}
	}
	return members
}

// BGPNeighbors returns BGP neighbors configured on this interface.
// For direct peering, this looks for neighbors using this interface's IP as local_addr.
func (i *Interface) BGPNeighbors() []string {
	configDB := i.node.ConfigDB()
	ipAddresses := i.IPAddresses()
	if configDB == nil || len(ipAddresses) == 0 {
		return nil
	}

	// Get the interface's IP without mask
	localIP := ipAddresses[0]
	if idx := strings.Index(localIP, "/"); idx > 0 {
		localIP = localIP[:idx]
	}

	var neighbors []string
	for neighborIP, neighbor := range configDB.BGPNeighbor {
		if neighbor.LocalAddr == localIP {
			neighbors = append(neighbors, neighborIP)
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
// Helpers
// ============================================================================

// extractServiceFromACL extracts service name from ACL naming convention.
// ACL name format: {service}-{direction} (per-service ACLs, shared across interfaces)
func extractServiceFromACL(aclName string) string {
	if strings.HasSuffix(aclName, "-in") {
		return strings.TrimSuffix(aclName, "-in")
	}
	if strings.HasSuffix(aclName, "-out") {
		return strings.TrimSuffix(aclName, "-out")
	}
	return ""
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
