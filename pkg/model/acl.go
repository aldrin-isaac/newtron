package model

// ACLTable represents an ACL table bound to interface(s)
type ACLTable struct {
	Name        string     `json:"name"`
	Type        string     `json:"type"` // L3, L3V6, MIRROR, MIRRORV6
	Description string     `json:"description"`
	Stage       string     `json:"stage"`              // ingress, egress
	Ports       []string   `json:"ports"`              // Bound interfaces
	Services    []string   `json:"services,omitempty"` // SONiC services (e.g., SSH, SNMP)
	Rules       []*ACLRule `json:"rules,omitempty"`
}

// ACLRule represents a single ACL rule
type ACLRule struct {
	Name        string `json:"name"`     // Rule name (e.g., "RULE_10")
	Priority    int    `json:"priority"` // Higher = matched first
	Description string `json:"description,omitempty"`

	// Match conditions
	SrcIP          string `json:"src_ip,omitempty"`            // Source IP/CIDR
	DstIP          string `json:"dst_ip,omitempty"`            // Destination IP/CIDR
	IPProtocol     int    `json:"ip_protocol,omitempty"`       // 6=TCP, 17=UDP, 1=ICMP, 89=OSPF, etc.
	L4SrcPort      int    `json:"l4_src_port,omitempty"`       // Single source port
	L4DstPort      int    `json:"l4_dst_port,omitempty"`       // Single destination port
	L4SrcPortRange string `json:"l4_src_port_range,omitempty"` // "1024-65535"
	L4DstPortRange string `json:"l4_dst_port_range,omitempty"`
	TCPFlags       string `json:"tcp_flags,omitempty"` // TCP flags to match
	DSCP           int    `json:"dscp,omitempty"`      // DSCP value (0-63)
	ICMPType       int    `json:"icmp_type,omitempty"`
	ICMPCode       int    `json:"icmp_code,omitempty"`
	EtherType      string `json:"ether_type,omitempty"` // For L2 ACLs
	InPorts        string `json:"in_ports,omitempty"`   // Ingress ports

	// Actions
	PacketAction string `json:"packet_action"`           // FORWARD, DROP, REDIRECT, DO_NOT_NAT
	RedirectPort string `json:"redirect_port,omitempty"` // For REDIRECT action
	MirrorAction string `json:"mirror_action,omitempty"` // For mirroring

	// QoS actions
	SetDSCP int    `json:"set_dscp,omitempty"` // Remark DSCP
	SetTC   int    `json:"set_tc,omitempty"`   // Set traffic class
	Policer string `json:"policer,omitempty"`  // Apply policer
}

// ACLTableType represents custom ACL table type definitions
type ACLTableType struct {
	Name          string   `json:"name"`
	MatchFields   []string `json:"match_fields"`    // Fields that can be matched
	Actions       []string `json:"actions"`         // Allowed actions
	BindPointType string   `json:"bind_point_type"` // PORT, PORTCHANNEL, etc.
}

// ACLType constants
const (
	ACLTypeL3       = "L3"
	ACLTypeL3V6     = "L3V6"
	ACLTypeMirror   = "MIRROR"
	ACLTypeMirrorV6 = "MIRRORV6"
	ACLTypeL2       = "L2"
)

// ACLStage constants
const (
	ACLStageIngress = "ingress"
	ACLStageEgress  = "egress"
)

// ACLAction constants
const (
	ACLActionForward  = "FORWARD"
	ACLActionDrop     = "DROP"
	ACLActionRedirect = "REDIRECT"
	ACLActionDoNotNAT = "DO_NOT_NAT"
)

// IP Protocol numbers
const (
	ProtocolICMP = 1
	ProtocolTCP  = 6
	ProtocolUDP  = 17
	ProtocolOSPF = 89
	ProtocolVRRP = 112
	ProtocolBGP  = 179 // This is a port number, not protocol
)

// ProtocolFromName converts protocol name to number
func ProtocolFromName(name string) int {
	switch name {
	case "icmp":
		return ProtocolICMP
	case "tcp":
		return ProtocolTCP
	case "udp":
		return ProtocolUDP
	case "ospf":
		return ProtocolOSPF
	case "vrrp":
		return ProtocolVRRP
	default:
		return 0
	}
}

// NewACLTable creates a new ACL table
func NewACLTable(name, aclType, stage string) *ACLTable {
	return &ACLTable{
		Name:  name,
		Type:  aclType,
		Stage: stage,
	}
}

// NewACLRule creates a new ACL rule
func NewACLRule(name string, priority int, action string) *ACLRule {
	return &ACLRule{
		Name:         name,
		Priority:     priority,
		PacketAction: action,
	}
}

// AddRule adds a rule to the ACL table
func (t *ACLTable) AddRule(rule *ACLRule) {
	// Insert in priority order (highest first)
	for i, r := range t.Rules {
		if rule.Priority > r.Priority {
			t.Rules = append(t.Rules[:i], append([]*ACLRule{rule}, t.Rules[i:]...)...)
			return
		}
	}
	t.Rules = append(t.Rules, rule)
}

// RemoveRule removes a rule from the ACL table
func (t *ACLTable) RemoveRule(name string) bool {
	for i, r := range t.Rules {
		if r.Name == name {
			t.Rules = append(t.Rules[:i], t.Rules[i+1:]...)
			return true
		}
	}
	return false
}

// GetRule returns a rule by name
func (t *ACLTable) GetRule(name string) *ACLRule {
	for _, r := range t.Rules {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// BindInterface binds the ACL to an interface.
func (t *ACLTable) BindInterface(iface string) {
	for _, p := range t.Ports {
		if p == iface {
			return
		}
	}
	t.Ports = append(t.Ports, iface)
}

// UnbindInterface removes an interface binding.
func (t *ACLTable) UnbindInterface(iface string) bool {
	for i, p := range t.Ports {
		if p == iface {
			t.Ports = append(t.Ports[:i], t.Ports[i+1:]...)
			return true
		}
	}
	return false
}

// IsBoundTo returns true if the ACL is bound to the given interface.
func (t *ACLTable) IsBoundTo(iface string) bool {
	for _, p := range t.Ports {
		if p == iface {
			return true
		}
	}
	return false
}
