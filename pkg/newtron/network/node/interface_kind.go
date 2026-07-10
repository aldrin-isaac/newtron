package node

import "strings"

// ============================================================================
// Interface kinds and capabilities — the domain model behind the per-kind
// operation gates.
//
// DESIGN_PRINCIPLES_NEWTRON.md §6 enumerates the per-interface feature
// surfaces: "VRF binding, VLAN membership, ACL application, QoS scheduling,
// BGP peering — all are per-interface." Those ARE the capability vocabulary;
// each capability below is one such surface, and each interface kind
// provides the subset its nature supports. Ops declare what they need
// (OpSpec.Needs); the precondition layer checks needs against the kind's
// capabilities before any write logic runs (§13: prevent, don't detect).
//
// This file is domain-only: no CONFIG_DB table names (§32). Which table
// realizes a capability for a kind is delivery knowledge and lives with the
// config generators (interface_config.go).
// ============================================================================

// InterfaceKind classifies an interface by its name family. Classification
// is purely lexical (the SONiC name families are disjoint); whether an
// interface of that kind EXISTS is a separate, kind-specific question
// answered by InterfaceExists against the PORT table or the intent DB.
type InterfaceKind int

const (
	KindUnknown InterfaceKind = iota
	// KindEthernet — a physical port. Exists a priori (hardware / PORT table).
	KindEthernet
	// KindPortChannel — an aggregate. Exists once created (create-portchannel).
	KindPortChannel
	// KindIRB — a VLAN's L3 face (SVI). Exists once its VLAN exists;
	// its routed identity is authored via configure-irb.
	KindIRB
	// KindLoopback — device-scoped L3 anchor; owned by baseline ops
	// (setup-device), not interface ops. No interface-op capabilities.
	KindLoopback
)

// String returns the kind's name for error messages and tests.
func (k InterfaceKind) String() string {
	switch k {
	case KindEthernet:
		return "physical port"
	case KindPortChannel:
		return "PortChannel"
	case KindIRB:
		return "VLAN interface (IRB)"
	case KindLoopback:
		return "Loopback"
	default:
		return "unknown interface kind"
	}
}

// interfaceKindOf classifies a (normalized) interface name into its kind.
func interfaceKindOf(name string) InterfaceKind {
	switch {
	case strings.HasPrefix(name, "Ethernet"):
		return KindEthernet
	case strings.HasPrefix(name, "PortChannel"):
		return KindPortChannel
	case strings.HasPrefix(name, "Vlan"):
		return KindIRB
	case strings.HasPrefix(name, "Loopback"):
		return KindLoopback
	default:
		return KindUnknown
	}
}

// Kind returns this interface's kind.
func (i *Interface) Kind() InterfaceKind {
	return interfaceKindOf(i.name)
}

// InterfaceCapability is one per-interface feature surface from §6's
// enumeration. An operation needs one or more; a kind provides a set.
type InterfaceCapability int

const (
	// CapabilityRouting — the interface can hold an L3 identity
	// (IP addressing, VRF binding).
	CapabilityRouting InterfaceCapability = iota
	// CapabilityVLANMembership — the interface can join a VLAN as an
	// access or trunk member.
	CapabilityVLANMembership
	// CapabilityACLBinding — the interface is a legal ACL bind point.
	// SONiC binds ACLs to ports and LAGs only (sonic-acl.yang ports:
	// PORT ∪ PORTCHANNEL; aclorch aclBindPointTypeLookup has exactly
	// those two entries) — VLAN interfaces are not bind points.
	CapabilityACLBinding
	// CapabilityQoSBinding — the interface is a legal QoS policy target.
	// SONiC's PORT_QOS_MAP key is "global" or a PORT leafref
	// (sonic-port-qos-map.yang) — physical ports only.
	CapabilityQoSBinding
	// CapabilityBGPPeering — a BGP peer can be derived from the
	// interface's IP (the interface IP is the session's update-source).
	CapabilityBGPPeering
	// CapabilityPortProperties — the interface owns a port row whose
	// properties (admin status, MTU, ...) can be set per interface.
	CapabilityPortProperties
)

// String returns the capability's §6-vocabulary name for error messages.
func (c InterfaceCapability) String() string {
	switch c {
	case CapabilityRouting:
		return "routing"
	case CapabilityVLANMembership:
		return "VLAN membership"
	case CapabilityACLBinding:
		return "ACL binding"
	case CapabilityQoSBinding:
		return "QoS binding"
	case CapabilityBGPPeering:
		return "BGP peering"
	case CapabilityPortProperties:
		return "port properties"
	default:
		return "unknown capability"
	}
}

// kindCapabilities is the capability matrix — which feature surfaces each
// interface kind provides by nature. Cells were settled empirically
// (validated suites, sonic yang models, orchagent source); see the matrix
// in api.md "Interface kinds and operation applicability" for the per-cell
// justifications.
var kindCapabilities = map[InterfaceKind]map[InterfaceCapability]bool{
	KindEthernet: {
		CapabilityRouting:        true,
		CapabilityVLANMembership: true,
		CapabilityACLBinding:     true,
		CapabilityQoSBinding:     true,
		CapabilityBGPPeering:     true,
		CapabilityPortProperties: true,
	},
	KindPortChannel: {
		CapabilityRouting:        true,
		CapabilityVLANMembership: true,
		CapabilityACLBinding:     true,
		// QoS: PORT_QOS_MAP accepts physical ports only (see
		// CapabilityQoSBinding) — LAG QoS is per-member in SONiC.
		CapabilityBGPPeering:     true,
		CapabilityPortProperties: true,
	},
	KindIRB: {
		// Routing is the IRB's nature — but its routed identity is
		// authored via configure-irb (VLAN noun), not configure-interface;
		// see capabilityAuthoring.
		CapabilityRouting:    true,
		CapabilityBGPPeering: true,
	},
	// KindLoopback, KindUnknown: no capabilities — every gated op refuses.
}

// HasCapability reports whether the kind provides the capability.
func (k InterfaceKind) HasCapability(c InterfaceCapability) bool {
	return kindCapabilities[k][c]
}

// capabilityAuthoring names the designed authoring path when a kind
// provides a capability but a DIFFERENT operation owns its authoring —
// refusal messages redirect instead of denying the capability's existence.
// One entry today: the IRB's routed identity belongs to the vlan noun
// (VLAN_INTERFACE is vlan_ops' table, §27).
var capabilityAuthoring = map[InterfaceKind]map[InterfaceCapability]string{
	KindIRB: {
		CapabilityRouting: "configure-irb (vlan noun)",
	},
}

// authoringOwner returns the named authoring path for (kind, capability),
// or "" when the interface-op path itself is the owner.
func authoringOwner(k InterfaceKind, c InterfaceCapability) string {
	return capabilityAuthoring[k][c]
}

// propertyApplicability records which kinds each set-property property
// applies to — the per-property granularity within CapabilityPortProperties.
// speed and description exist only on the physical PORT row (the
// PORTCHANNEL row has admin_status, mtu, min_links, fallback, fast_rate —
// sonic-portchannel.yang).
var propertyApplicability = map[string]map[InterfaceKind]bool{
	"mtu":          {KindEthernet: true, KindPortChannel: true},
	"admin_status": {KindEthernet: true, KindPortChannel: true},
	"admin-status": {KindEthernet: true, KindPortChannel: true},
	"speed":        {KindEthernet: true},
	"description":  {KindEthernet: true},
}

// propertyAppliesTo reports whether the property can be set on the kind.
func propertyAppliesTo(property string, k InterfaceKind) bool {
	return propertyApplicability[property][k]
}
