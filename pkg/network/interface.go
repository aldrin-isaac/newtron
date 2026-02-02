package network

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/newtron-network/newtron/pkg/spec"
)

// Interface represents a network interface within the context of a Device.
//
// Key design: Interface has a parent reference to Device, which in turn has
// a parent reference to Network. This provides hierarchical access:
//
//	intf.Device()                          // Get parent Device
//	intf.Device().Network()                // Get Network from Device
//	intf.Device().Network().GetService()   // Access Network-level config
//	intf.Device().ASNumber()               // Access Device-level config
//
// This mirrors the original Perl design where interface operations had
// implicit access to node and network-level configuration.
//
// Hierarchy: Network -> Device -> Interface
type Interface struct {
	// Parent reference - provides access to Device and Network configuration
	device *Device

	// Interface identity
	name string

	// Current state (from config_db)
	adminStatus string
	operStatus  string
	speed       string
	mtu         int
	vrf         string
	ipAddresses []string

	// Service binding (from NEWTRON_SERVICE_BINDING table)
	serviceName   string
	serviceIP     string // IP address assigned by service
	serviceVRF    string // VRF created by service
	serviceIPVPN  string // IP-VPN name
	serviceMACVPN string // MAC-VPN name
	ingressACL    string
	egressACL     string

	// LAG membership
	lagMember string // Parent LAG if this is a member
}

// ============================================================================
// Parent Accessors - Key to OO Design
// ============================================================================

// Device returns the parent Device object.
// This allows access to Device-level properties and Network configuration.
//
// Example usage:
//
//	asNum := intf.Device().ASNumber()
//	neighbors := intf.Device().BGPNeighbors()
func (i *Interface) Device() *Device {
	return i.device
}

// Network returns the Network object (via Device parent).
// Convenience method for accessing Network-level configuration.
//
// Example usage:
//
//	svc, _ := intf.Network().GetService("customer-l3")
//	filter, _ := intf.Network().GetFilterSpec("customer-edge-in")
func (i *Interface) Network() *Network {
	return i.device.Network()
}

// ============================================================================
// Interface Properties
// ============================================================================

// Name returns the interface name (e.g., "Ethernet0", "PortChannel100").
func (i *Interface) Name() string {
	return i.name
}

// AdminStatus returns the administrative status (up/down).
func (i *Interface) AdminStatus() string {
	return i.adminStatus
}

// OperStatus returns the operational status (up/down).
func (i *Interface) OperStatus() string {
	return i.operStatus
}

// Speed returns the interface speed.
func (i *Interface) Speed() string {
	return i.speed
}

// MTU returns the interface MTU.
func (i *Interface) MTU() int {
	return i.mtu
}

// VRF returns the VRF this interface is bound to.
func (i *Interface) VRF() string {
	return i.vrf
}

// IPAddresses returns the IP addresses configured on this interface.
func (i *Interface) IPAddresses() []string {
	return i.ipAddresses
}

// ============================================================================
// Service Binding
// ============================================================================

// ServiceName returns the name of the service bound to this interface.
func (i *Interface) ServiceName() string {
	return i.serviceName
}

// HasService returns true if a service is bound to this interface.
func (i *Interface) HasService() bool {
	return i.serviceName != ""
}

// Service returns the service definition for the bound service.
// Returns nil if no service is bound.
func (i *Interface) Service() *spec.ServiceSpec {
	if i.serviceName == "" {
		return nil
	}
	svc, err := i.Network().GetService(i.serviceName)
	if err != nil {
		return nil
	}
	return svc
}

// ServiceIP returns the IP address assigned by the service.
func (i *Interface) ServiceIP() string {
	return i.serviceIP
}

// ServiceVRF returns the VRF name created/bound by the service.
func (i *Interface) ServiceVRF() string {
	return i.serviceVRF
}

// ServiceIPVPN returns the IP-VPN name bound by the service.
func (i *Interface) ServiceIPVPN() string {
	return i.serviceIPVPN
}

// ServiceMACVPN returns the MAC-VPN name bound by the service.
func (i *Interface) ServiceMACVPN() string {
	return i.serviceMACVPN
}

// IngressACL returns the name of the ingress ACL bound to this interface.
func (i *Interface) IngressACL() string {
	return i.ingressACL
}

// EgressACL returns the name of the egress ACL bound to this interface.
func (i *Interface) EgressACL() string {
	return i.egressACL
}

// ============================================================================
// LAG Membership
// ============================================================================

// IsLAGMember returns true if this interface is a LAG member.
func (i *Interface) IsLAGMember() bool {
	return i.lagMember != ""
}

// LAGParent returns the name of the parent LAG (if this is a member).
func (i *Interface) LAGParent() string {
	return i.lagMember
}

// Description returns the interface description (from PORT table).
func (i *Interface) Description() string {
	configDB := i.device.ConfigDB()
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

// LAGMembers returns the member interfaces if this is a PortChannel.
func (i *Interface) LAGMembers() []string {
	if !i.IsPortChannel() {
		return nil
	}
	configDB := i.device.ConfigDB()
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
	configDB := i.device.ConfigDB()
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
	configDB := i.device.ConfigDB()
	if configDB == nil || len(i.ipAddresses) == 0 {
		return nil
	}

	// Get the interface's IP without mask
	localIP := i.ipAddresses[0]
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

// IsPhysical returns true if this is a physical interface (Ethernet*).
func (i *Interface) IsPhysical() bool {
	return strings.HasPrefix(i.name, "Ethernet")
}

// IsPortChannel returns true if this is a port channel (PortChannel*).
func (i *Interface) IsPortChannel() bool {
	return strings.HasPrefix(i.name, "PortChannel")
}

// IsVLAN returns true if this is a VLAN interface (Vlan*).
func (i *Interface) IsVLAN() bool {
	return strings.HasPrefix(i.name, "Vlan")
}

// IsLoopback returns true if this is a loopback interface.
func (i *Interface) IsLoopback() bool {
	return strings.HasPrefix(i.name, "Loopback")
}

// ============================================================================
// State Loading
// ============================================================================

// loadState populates interface state from config_db and state_db.
func (i *Interface) loadState() {
	configDB := i.device.ConfigDB()
	if configDB == nil {
		return
	}

	// Load from PORT table (config_db)
	if port, ok := configDB.Port[i.name]; ok {
		i.adminStatus = port.AdminStatus
		i.speed = port.Speed
		if port.MTU != "" {
			i.mtu, _ = strconv.Atoi(port.MTU)
		}
	}

	// Load from PORTCHANNEL table (config_db)
	if pc, ok := configDB.PortChannel[i.name]; ok {
		i.adminStatus = pc.AdminStatus
		if pc.MTU != "" {
			i.mtu, _ = strconv.Atoi(pc.MTU)
		}
	}

	// Load operational state from state_db (if available)
	if i.device.Underlying() != nil && i.device.Underlying().StateDB != nil {
		stateDB := i.device.Underlying().StateDB
		// Get operational status from PORT_TABLE in state_db
		if portState, ok := stateDB.PortTable[i.name]; ok {
			i.operStatus = portState.OperStatus
			// Override speed/MTU with operational values if available
			if portState.Speed != "" {
				i.speed = portState.Speed
			}
			if portState.MTU != "" {
				i.mtu, _ = strconv.Atoi(portState.MTU)
			}
		}
		// Get LAG operational status from LAG_TABLE
		if lagState, ok := stateDB.LAGTable[i.name]; ok {
			i.operStatus = lagState.OperStatus
		}
	}

	// Load VRF binding from INTERFACE table
	if intf, ok := configDB.Interface[i.name]; ok {
		i.vrf = intf.VRFName
	}

	// Load IP addresses from INTERFACE table
	for key := range configDB.Interface {
		// Keys can be "Ethernet0" or "Ethernet0|10.1.1.1/30"
		if strings.HasPrefix(key, i.name+"|") {
			ipAddr := strings.TrimPrefix(key, i.name+"|")
			i.ipAddresses = append(i.ipAddresses, ipAddr)
		}
	}

	// Check LAG membership
	for key := range configDB.PortChannelMember {
		// Key format: PortChannel100|Ethernet0
		parts := strings.SplitN(key, "|", 2)
		if len(parts) == 2 && parts[1] == i.name {
			i.lagMember = parts[0]
			break
		}
	}

	// Load ACL bindings from ACL_TABLE
	for aclName, acl := range configDB.ACLTable {
		ports := strings.Split(acl.Ports, ",")
		for _, port := range ports {
			if strings.TrimSpace(port) == i.name {
				if acl.Stage == "ingress" {
					i.ingressACL = aclName
				} else if acl.Stage == "egress" {
					i.egressACL = aclName
				}
			}
		}
	}

	// Load service binding from NEWTRON_SERVICE_BINDING table (preferred)
	if binding, ok := configDB.NewtronServiceBinding[i.name]; ok {
		i.serviceName = binding.ServiceName
		i.serviceIP = binding.IPAddress
		i.serviceVRF = binding.VRFName
		i.serviceIPVPN = binding.IPVPN
		i.serviceMACVPN = binding.MACVPN
		// ACLs from binding override detected ones
		if binding.IngressACL != "" {
			i.ingressACL = binding.IngressACL
		}
		if binding.EgressACL != "" {
			i.egressACL = binding.EgressACL
		}
	} else if i.ingressACL != "" {
		// Fallback: detect service binding from ACL naming convention
		// Service name is encoded in ACL name: {service}-{interface}-{direction}
		i.serviceName = i.extractServiceFromACL(i.ingressACL)
	}
}

// extractServiceFromACL extracts service name from ACL naming convention.
// ACL name format: {service}-{direction} (per-service ACLs, shared across interfaces)
func (i *Interface) extractServiceFromACL(aclName string) string {
	// Remove the direction suffix
	if strings.HasSuffix(aclName, "-in") {
		return strings.TrimSuffix(aclName, "-in")
	}
	if strings.HasSuffix(aclName, "-out") {
		return strings.TrimSuffix(aclName, "-out")
	}
	return ""
}

// ============================================================================
// MAC-VPN (L2 EVPN) Information
// ============================================================================

// MACVPNInfo returns MAC-VPN binding information for this interface.
// Only valid for VLAN interfaces. Returns nil for non-VLAN interfaces.
func (i *Interface) MACVPNInfo() *MACVPNInfo {
	if !i.IsVLAN() {
		return nil
	}

	configDB := i.device.ConfigDB()
	if configDB == nil {
		return nil
	}

	vlanName := i.name // e.g., "Vlan100"

	info := &MACVPNInfo{}

	// Get L2VNI from VXLAN_TUNNEL_MAP
	for key, mapping := range configDB.VXLANTunnelMap {
		if mapping.VLAN == vlanName {
			vni, _ := strconv.Atoi(mapping.VNI)
			info.L2VNI = vni

			// Try to match to a macvpn definition by L2VNI
			network := i.Network()
			if network != nil && network.spec != nil {
				for macvpnName, macvpnDef := range network.spec.MACVPN {
					if macvpnDef.L2VNI == vni {
						info.Name = macvpnName
						break
					}
				}
			}
			_ = key // suppress unused warning
			break
		}
	}

	// Check ARP suppression
	if _, ok := configDB.SuppressVLANNeigh[vlanName]; ok {
		info.ARPSuppression = true
	}

	if info.L2VNI == 0 {
		return nil
	}

	return info
}

// ============================================================================
// String Representation
// ============================================================================

// String returns a string representation of the interface.
func (i *Interface) String() string {
	status := "down"
	if i.adminStatus == "up" && i.operStatus == "up" {
		status = "up"
	} else if i.adminStatus == "up" {
		status = "admin-up/oper-down"
	}

	desc := fmt.Sprintf("%s (%s)", i.name, status)

	if i.serviceName != "" {
		desc += fmt.Sprintf(" [service: %s]", i.serviceName)
	}
	if len(i.ipAddresses) > 0 {
		desc += fmt.Sprintf(" [ip: %s]", strings.Join(i.ipAddresses, ", "))
	}
	if i.vrf != "" {
		desc += fmt.Sprintf(" [vrf: %s]", i.vrf)
	}
	if i.lagMember != "" {
		desc += fmt.Sprintf(" [member of: %s]", i.lagMember)
	}

	return desc
}
