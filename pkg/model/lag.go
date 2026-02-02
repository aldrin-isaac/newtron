package model

// PortChannel represents a link aggregation group (LAG)
type PortChannel struct {
	Name        string   `json:"name"` // e.g., "PortChannel100"
	Description string   `json:"description"`
	MTU         int      `json:"mtu"`
	AdminStatus string   `json:"admin_status"` // up, down
	MinLinks    int      `json:"min_links"`
	Fallback    bool     `json:"fallback"`
	FastRate    bool     `json:"fast_rate"` // LACP fast (1s) vs slow (30s)
	Members     []string `json:"members"`   // Physical interfaces
	Mode        string   `json:"mode"`      // active, passive, on (static)

	// L3 configuration (inherited from Interface when used as L3)
	VRF       string   `json:"vrf,omitempty"`
	IPv4Addrs []string `json:"ipv4_addrs,omitempty"`

	// L2 configuration
	SwitchportMode string `json:"switchport_mode,omitempty"` // access, trunk
	AccessVLAN     int    `json:"access_vlan,omitempty"`
	TrunkVLANs     []int  `json:"trunk_vlans,omitempty"`

	// Service binding
	Service string `json:"service,omitempty"`
}

// LACPMode represents LACP negotiation mode
type LACPMode string

const (
	LACPModeActive  LACPMode = "active"  // Actively sends LACP PDUs
	LACPModePassive LACPMode = "passive" // Only responds to LACP PDUs
	LACPModeOn      LACPMode = "on"      // Static LAG, no LACP
)

// PortChannelMember represents a LAG member interface
type PortChannelMember struct {
	Interface   string `json:"interface"`
	PortChannel string `json:"port_channel"`
	LACPState   string `json:"lacp_state,omitempty"` // bundled, standby, suspended
}

// PortChannelState represents the operational state of a LAG
type PortChannelState struct {
	Name           string            `json:"name"`
	OperStatus     string            `json:"oper_status"`
	ActiveMembers  []string          `json:"active_members"`
	StandbyMembers []string          `json:"standby_members,omitempty"`
	MemberStates   []LACPMemberState `json:"member_states"`
}

// LACPMemberState represents LACP state for a single member
type LACPMemberState struct {
	Interface    string `json:"interface"`
	Selected     bool   `json:"selected"`
	ActorState   string `json:"actor_state"` // Activity, Timeout, Aggregation, etc.
	PartnerState string `json:"partner_state"`
	ActorPort    int    `json:"actor_port"`
	PartnerPort  int    `json:"partner_port"`
	ActorKey     int    `json:"actor_key"`
	PartnerKey   int    `json:"partner_key"`
}

// NewPortChannel creates a new PortChannel with defaults
func NewPortChannel(name string, members []string) *PortChannel {
	return &PortChannel{
		Name:        name,
		Members:     members,
		MinLinks:    1,
		Mode:        string(LACPModeActive),
		FastRate:    true,
		MTU:         9100,
		AdminStatus: "up",
	}
}

// AddMember adds a member interface to the LAG
func (p *PortChannel) AddMember(iface string) {
	for _, m := range p.Members {
		if m == iface {
			return // Already a member
		}
	}
	p.Members = append(p.Members, iface)
}

// RemoveMember removes a member interface from the LAG
func (p *PortChannel) RemoveMember(iface string) bool {
	for i, m := range p.Members {
		if m == iface {
			p.Members = append(p.Members[:i], p.Members[i+1:]...)
			return true
		}
	}
	return false
}

// HasMember returns true if the interface is a member
func (p *PortChannel) HasMember(iface string) bool {
	for _, m := range p.Members {
		if m == iface {
			return true
		}
	}
	return false
}

// MemberCount returns the number of member interfaces
func (p *PortChannel) MemberCount() int {
	return len(p.Members)
}
