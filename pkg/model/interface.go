// Package model defines the domain models for network configuration.
package model

import "time"

// Interface represents a network interface
type Interface struct {
	Name        string `json:"name"` // e.g., "Ethernet0", "PortChannel100"
	Description string `json:"description"`
	AdminStatus string `json:"admin_status"` // up, down
	OperStatus  string `json:"oper_status"`  // up, down (read-only)
	Speed       string `json:"speed"`        // 1G, 10G, 25G, 40G, 100G, auto
	MTU         int    `json:"mtu"`
	FEC         string `json:"fec"`     // none, rs, fc, auto
	Autoneg     string `json:"autoneg"` // on, off
	Lanes       string `json:"lanes"`   // Physical lanes

	// L3 configuration
	VRF       string   `json:"vrf,omitempty"`
	IPv4Addrs []string `json:"ipv4_addrs,omitempty"`
	IPv6Addrs []string `json:"ipv6_addrs,omitempty"`

	// L2 configuration
	Mode       string `json:"mode,omitempty"` // access, trunk, routed
	AccessVLAN int    `json:"access_vlan,omitempty"`
	TrunkVLANs []int  `json:"trunk_vlans,omitempty"`
	NativeVLAN int    `json:"native_vlan,omitempty"`

	// Membership
	LAG string `json:"lag,omitempty"` // Parent PortChannel if member

	// Service binding
	Service string `json:"service,omitempty"` // Applied service name

	// ACL bindings
	IngressACL string `json:"ingress_acl,omitempty"`
	EgressACL  string `json:"egress_acl,omitempty"`

	// QoS
	QoSProfile string `json:"qos_profile,omitempty"`
	TrustDSCP  bool   `json:"trust_dscp,omitempty"`

	// Statistics (read-only)
	Stats *InterfaceStats `json:"stats,omitempty"`
}

// InterfaceStats contains interface counters
type InterfaceStats struct {
	RxBytes     uint64    `json:"rx_bytes"`
	TxBytes     uint64    `json:"tx_bytes"`
	RxPackets   uint64    `json:"rx_packets"`
	TxPackets   uint64    `json:"tx_packets"`
	RxErrors    uint64    `json:"rx_errors"`
	TxErrors    uint64    `json:"tx_errors"`
	RxDrops     uint64    `json:"rx_drops"`
	TxDrops     uint64    `json:"tx_drops"`
	LastCleared time.Time `json:"last_cleared"`
	LastUpdated time.Time `json:"last_updated"`
}

// InterfaceState represents the operational state of an interface
type InterfaceState struct {
	Name       string `json:"name"`
	OperStatus string `json:"oper_status"`
	Speed      string `json:"speed"`
	Duplex     string `json:"duplex"`
	LinkUp     bool   `json:"link_up"`
}

// IsPhysical returns true if this is a physical interface
func (i *Interface) IsPhysical() bool {
	return len(i.Name) > 0 && (i.Name[0] == 'E' || i.Name[0] == 'e')
}

// IsLAG returns true if this is a LAG/PortChannel interface
func (i *Interface) IsLAG() bool {
	return len(i.Name) > 0 && (i.Name[0] == 'P' || i.Name[0] == 'p')
}

// IsVLAN returns true if this is a VLAN interface (SVI)
func (i *Interface) IsVLAN() bool {
	return len(i.Name) > 0 && (i.Name[0] == 'V' || i.Name[0] == 'v')
}

// IsLoopback returns true if this is a loopback interface
func (i *Interface) IsLoopback() bool {
	return len(i.Name) > 0 && (i.Name[0] == 'L' || i.Name[0] == 'l')
}

// IsLAGMember returns true if this interface is a member of a LAG
func (i *Interface) IsLAGMember() bool {
	return i.LAG != ""
}

// HasService returns true if a service is applied to this interface
func (i *Interface) HasService() bool {
	return i.Service != ""
}

// HasIPAddress returns true if the interface has at least one IP address
func (i *Interface) HasIPAddress() bool {
	return len(i.IPv4Addrs) > 0 || len(i.IPv6Addrs) > 0
}

// IsRouted returns true if this is a routed (L3) interface
func (i *Interface) IsRouted() bool {
	return i.Mode == "routed" || i.Mode == "" && i.HasIPAddress()
}

// IsSwitched returns true if this is a switched (L2) interface
func (i *Interface) IsSwitched() bool {
	return i.Mode == "access" || i.Mode == "trunk"
}
