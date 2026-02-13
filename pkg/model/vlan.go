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
	TaggedMembers   []string `json:"tagged_members,omitempty"`
	UntaggedMembers []string `json:"untagged_members,omitempty"`

	// DHCP Relay
	DHCPRelayAddrs []string `json:"dhcp_relay_addrs,omitempty"`

	// Service binding
	Service string `json:"service,omitempty"`
}

// VLANMember represents VLAN membership for an interface
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
	ActiveMembers []string `json:"active_members"`
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

// AddTaggedMember adds a tagged member to the VLAN.
func (v *VLAN) AddTaggedMember(iface string) {
	for _, m := range v.TaggedMembers {
		if m == iface {
			return
		}
	}
	v.TaggedMembers = append(v.TaggedMembers, iface)
}

// AddUntaggedMember adds an untagged member to the VLAN.
func (v *VLAN) AddUntaggedMember(iface string) {
	for _, m := range v.UntaggedMembers {
		if m == iface {
			return
		}
	}
	v.UntaggedMembers = append(v.UntaggedMembers, iface)
}

// RemoveMember removes a member from the VLAN (both tagged and untagged).
func (v *VLAN) RemoveMember(iface string) bool {
	removed := false
	for i, m := range v.TaggedMembers {
		if m == iface {
			v.TaggedMembers = append(v.TaggedMembers[:i], v.TaggedMembers[i+1:]...)
			removed = true
			break
		}
	}
	for i, m := range v.UntaggedMembers {
		if m == iface {
			v.UntaggedMembers = append(v.UntaggedMembers[:i], v.UntaggedMembers[i+1:]...)
			removed = true
			break
		}
	}
	return removed
}

// HasMember returns true if the interface is a member of this VLAN.
func (v *VLAN) HasMember(iface string) bool {
	for _, m := range v.TaggedMembers {
		if m == iface {
			return true
		}
	}
	for _, m := range v.UntaggedMembers {
		if m == iface {
			return true
		}
	}
	return false
}
