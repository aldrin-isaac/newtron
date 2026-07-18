package spec

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// validate.go — the single owner of spec constraint validation (DESIGN_PRINCIPLES
// §15, "Symmetry is an axis, not a direction"; §27 single owner). Every
// constraint here is enforced by BOTH paths that produce a spec: the loader at
// load time and the write path before it persists. Because they call the same
// code, a write can never persist a spec the loader would reject (persist-load
// symmetry). Reference resolution is validated just as symmetrically, via the
// declarative MissingRefs (references.go); these methods cover the intrinsic
// constraints that `ref:` tags cannot express — required fields, value ranges
// and formats, enum membership, and internal uniqueness.

// ValidateConstraints checks a platform's port inventory — the single authority
// for which interfaces a node of this platform supports (§27), so it must be
// internally consistent. PortCount must equal the inventory length (the bound is
// the inventory), and every port needs a unique name and a NIC slot >= 1 (NIC 0
// is management). name is used in diagnostics.
func (p *PlatformSpec) ValidateConstraints(name string) error {
	v := &util.ValidationBuilder{}
	v.Add(p.PortCount == len(p.Ports),
		fmt.Sprintf("platform %q: port_count %d must equal the ports inventory length %d — the inventory is the authority for supported interfaces",
			name, p.PortCount, len(p.Ports)))
	seenName := make(map[string]bool, len(p.Ports))
	seenNIC := make(map[int]bool, len(p.Ports))
	for i, ps := range p.Ports {
		v.Add(ps.Name != "", fmt.Sprintf("platform %q: ports[%d] has an empty name", name, i))
		v.Add(!(ps.Name != "" && seenName[ps.Name]), fmt.Sprintf("platform %q: duplicate port name %q", name, ps.Name))
		seenName[ps.Name] = true
		v.Add(ps.NICIndex >= 1, fmt.Sprintf("platform %q: port %q nic_index %d is invalid (1-based; NIC 0 is management)", name, ps.Name, ps.NICIndex))
		v.Add(!(ps.NICIndex >= 1 && seenNIC[ps.NICIndex]), fmt.Sprintf("platform %q: nic_index %d is used by more than one port (%q)", name, ps.NICIndex, ps.Name))
		seenNIC[ps.NICIndex] = true
	}
	return v.Build()
}

// ValidateConstraints checks a QoS policy's intrinsic constraints: queue count,
// queue-name uniqueness, per-type weight rules, and DSCP range/uniqueness. name
// is used in diagnostics. Nil queue slots (the write path fills gaps by index)
// are skipped. (QoS policies carry no cross-spec references, so this is the whole
// of their validation.)
func (q *QoSPolicy) ValidateConstraints(name string) error {
	v := &util.ValidationBuilder{}
	q.validateConstraints(v, name)
	return v.Build()
}

// validateConstraints appends a QoS policy's constraint errors to a shared
// builder — the form the loader uses to accumulate errors across every policy in
// one pass.
func (q *QoSPolicy) validateConstraints(v *util.ValidationBuilder, name string) {
	// An empty policy is a valid shell — create-qos-policy authors one with no
	// queues, then add-qos-queue populates it. The meaningful structural checks
	// (uniqueness, count, weights, DSCP) apply to the queues that are present.
	queues := 0
	for _, qq := range q.Queues {
		if qq != nil {
			queues++
		}
	}
	if queues > 8 {
		v.AddErrorf("QoS policy '%s' has %d queues (max 8)", name, queues)
		return
	}

	seenDSCP := make(map[int]string)   // DSCP value → queue name (for dup detection)
	seenNames := make(map[string]bool) // queue name uniqueness
	for i, qq := range q.Queues {
		if qq == nil {
			continue
		}
		if qq.Name == "" {
			v.AddErrorf("QoS policy '%s' queue[%d] has empty name", name, i)
		} else if seenNames[qq.Name] {
			v.AddErrorf("QoS policy '%s' has duplicate queue name '%s'", name, qq.Name)
		}
		seenNames[qq.Name] = true

		switch qq.Type {
		case "dwrr":
			if qq.Weight <= 0 {
				v.AddErrorf("QoS policy '%s' queue '%s': DWRR requires weight > 0", name, qq.Name)
			}
		case "strict":
			if qq.Weight != 0 {
				v.AddErrorf("QoS policy '%s' queue '%s': strict queue must not have weight", name, qq.Name)
			}
		default:
			v.AddErrorf("QoS policy '%s' queue '%s': invalid type '%s' (must be dwrr or strict)", name, qq.Name, qq.Type)
		}

		for _, dscp := range qq.DSCP {
			if dscp < 0 || dscp > 63 {
				v.AddErrorf("QoS policy '%s' queue '%s': DSCP value %d out of range (0-63)", name, qq.Name, dscp)
			} else if prev, dup := seenDSCP[dscp]; dup {
				v.AddErrorf("QoS policy '%s': DSCP %d mapped to both '%s' and '%s'", name, dscp, prev, qq.Name)
			}
			seenDSCP[dscp] = qq.Name
		}
	}
}

// ValidateConstraints checks a service's type-level constraints — the references
// its service_type requires and that service_type is known. The references
// themselves (do they resolve?) are checked separately by MissingRefs; this
// covers which references each type mandates.
func (s *ServiceSpec) ValidateConstraints(name string) error {
	v := &util.ValidationBuilder{}
	s.validateConstraints(v, "", name)
	return v.Build()
}

func (s *ServiceSpec) validateConstraints(v *util.ValidationBuilder, prefix, name string) {
	switch s.ServiceType {
	case ServiceTypeEVPNIRB:
		if s.IPVPN == "" {
			v.AddErrorf("%sservice '%s' (evpn-irb) requires ipvpn reference", prefix, name)
		}
		if s.MACVPN == "" {
			v.AddErrorf("%sservice '%s' (evpn-irb) requires macvpn reference", prefix, name)
		}
	case ServiceTypeEVPNBridged:
		if s.MACVPN == "" {
			v.AddErrorf("%sservice '%s' (evpn-bridged) requires macvpn reference", prefix, name)
		}
	case ServiceTypeEVPNRouted:
		if s.IPVPN == "" {
			v.AddErrorf("%sservice '%s' (evpn-routed) requires ipvpn reference", prefix, name)
		}
	case ServiceTypeIRB, ServiceTypeBridged, ServiceTypeRouted:
		// Local types: no type-mandated references.
	default:
		v.AddErrorf("%sservice '%s' has unknown type '%s'", prefix, name, s.ServiceType)
	}

	// vrf_type=interface (a per-service, per-interface VRF) contradicts an ipvpn
	// reference: an ipvpn IS a shared VRF, and vrf_type=shared is what uses it.
	// With both set, the composite creates the per-interface VRF but binds the
	// ipvpn to its own Vrf_<ipvpn>, which does not exist — surfacing as a confusing
	// "VRF not found" deep in apply. Reject at author time (§13: schema and apply
	// agree on one rule) so the operator picks a model up front.
	if s.VRFType == VRFTypeInterface && s.IPVPN != "" {
		v.AddErrorf("%sservice '%s' sets vrf_type=interface but references ipvpn '%s' — an ipvpn is a shared VRF; use vrf_type=shared to bind the ipvpn's VRF, or remove the ipvpn to use a per-interface VRF", prefix, name, s.IPVPN)
	}
}

// ValidateConstraints checks a node spec's required fields and value formats.
// isHost relaxes the rules to a host device (only mgmt_ip required); knownZones
// is the set of zones the spec's `zone` must be one of (nil skips that check for
// callers that validate zone membership elsewhere).
func (n *NodeSpec) ValidateConstraints(isHost bool, knownZones map[string]*ZoneSpec) error {
	v := &util.ValidationBuilder{}

	if isHost {
		v.Add(n.MgmtIP != "", "mgmt_ip is required")
		if n.MgmtIP != "" && !util.IsValidIPv4(n.MgmtIP) {
			v.AddErrorf("invalid management IP: %s", n.MgmtIP)
		}
		return v.Build()
	}

	v.Add(n.MgmtIP != "", "mgmt_ip is required")
	v.Add(n.LoopbackIP != "", "loopback_ip is required")
	v.Add(n.Zone != "", "zone is required")

	if n.MgmtIP != "" && !util.IsValidIPv4(n.MgmtIP) {
		v.AddErrorf("invalid management IP: %s", n.MgmtIP)
	}
	if n.LoopbackIP != "" && !util.IsValidIPv4(n.LoopbackIP) {
		v.AddErrorf("invalid loopback IP: %s", n.LoopbackIP)
	}
	if n.Zone != "" && knownZones != nil {
		if _, ok := knownZones[n.Zone]; !ok {
			v.AddErrorf("unknown zone: %s", n.Zone)
		}
	}

	return v.Build()
}

// ValidateConstraints appends the constraint errors of every constraint-bearing
// spec in the set to a shared builder — the QoS policies and the services.
// prefix labels the scope in messages (e.g. "zone 'amer': "). It is the
// load-side aggregate of the per-object ValidateConstraints methods the write
// path calls individually.
func (o *OverridableSpecs) ValidateConstraints(v *util.ValidationBuilder, prefix string) {
	for name, policy := range o.QoSPolicies {
		policy.validateConstraints(v, prefix+name)
	}
	for name, svc := range o.Services {
		svc.validateConstraints(v, prefix, name)
	}
}
