package model

// VLAN represents a VLAN configuration
type VLAN struct {
	ID          int    `json:"id"`   // VLAN ID (1-4094)
	Name        string `json:"name"` // VLAN name
	Description string `json:"description"`
	AdminStatus string `json:"admin_status"` // up, down

	// L2VNI for EVPN
	L2VNI          int  `json:"l2_vni,omitempty"`
	ARPSuppression bool `json:"arp_suppression,omitempty"`

	// SVI configuration (for IRB)
	VRF            string `json:"vrf,omitempty"`          // VRF for SVI
	IPv4Address    string `json:"ipv4_address,omitempty"` // SVI IP address
	AnycastGateway string `json:"anycast_gateway,omitempty"`
	AnycastMAC     string `json:"anycast_mac,omitempty"`

	// Members
	TaggedPorts   []string `json:"tagged_ports,omitempty"`
	UntaggedPorts []string `json:"untagged_ports,omitempty"`

	// DHCP Relay
	DHCPRelayAddrs []string `json:"dhcp_relay_addrs,omitempty"`

	// Service binding
	Service string `json:"service,omitempty"`
}

// VLANMember represents VLAN membership for a port
type VLANMember struct {
	VLAN      int    `json:"vlan"`
	Interface string `json:"interface"`
	Tagging   string `json:"tagging"` // tagged, untagged
}

// VLANState represents the operational state of a VLAN
type VLANState struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	AdminStatus string   `json:"admin_status"`
	OperStatus  string   `json:"oper_status"`
	SVIStatus   string   `json:"svi_status,omitempty"`
	MACCount    int      `json:"mac_count"`
	ActivePorts []string `json:"active_ports"`
}

// NewVLAN creates a new VLAN with defaults
func NewVLAN(id int, name string) *VLAN {
	return &VLAN{
		ID:          id,
		Name:        name,
		AdminStatus: "up",
	}
}

// HasSVI returns true if this VLAN has an SVI configured
func (v *VLAN) HasSVI() bool {
	return v.IPv4Address != "" || v.AnycastGateway != ""
}

// HasEVPN returns true if this VLAN has EVPN (L2VNI) configured
func (v *VLAN) HasEVPN() bool {
	return v.L2VNI > 0
}

// IsIRB returns true if this VLAN is configured for IRB (L2 + L3)
func (v *VLAN) IsIRB() bool {
	return v.HasEVPN() && v.HasSVI()
}

// AddTaggedPort adds a tagged port to the VLAN
func (v *VLAN) AddTaggedPort(port string) {
	for _, p := range v.TaggedPorts {
		if p == port {
			return
		}
	}
	v.TaggedPorts = append(v.TaggedPorts, port)
}

// AddUntaggedPort adds an untagged port to the VLAN
func (v *VLAN) AddUntaggedPort(port string) {
	for _, p := range v.UntaggedPorts {
		if p == port {
			return
		}
	}
	v.UntaggedPorts = append(v.UntaggedPorts, port)
}

// RemovePort removes a port from the VLAN (both tagged and untagged)
func (v *VLAN) RemovePort(port string) bool {
	removed := false
	for i, p := range v.TaggedPorts {
		if p == port {
			v.TaggedPorts = append(v.TaggedPorts[:i], v.TaggedPorts[i+1:]...)
			removed = true
			break
		}
	}
	for i, p := range v.UntaggedPorts {
		if p == port {
			v.UntaggedPorts = append(v.UntaggedPorts[:i], v.UntaggedPorts[i+1:]...)
			removed = true
			break
		}
	}
	return removed
}

// HasPort returns true if the port is a member of this VLAN
func (v *VLAN) HasPort(port string) bool {
	for _, p := range v.TaggedPorts {
		if p == port {
			return true
		}
	}
	for _, p := range v.UntaggedPorts {
		if p == port {
			return true
		}
	}
	return false
}
