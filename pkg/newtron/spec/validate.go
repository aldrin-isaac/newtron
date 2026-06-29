package spec

import "github.com/aldrin-isaac/newtron/pkg/util"

// validate.go — the single owner of spec-shape validation (DESIGN_PRINCIPLES §15,
// "Symmetry is an axis, not a direction"; §27 single owner). Every invariant
// here is enforced by BOTH paths that produce a spec: the loader at load time
// and the write path before it persists. Because they call the same code, a
// write can never persist a spec the loader would reject (persist-load
// symmetry). Reference resolution is validated just as symmetrically, via the
// declarative MissingRefs (references.go); these methods cover the shape and
// structure that `ref:` tags cannot express.

// ValidateShape checks a QoS policy's structural invariants: queue count,
// queue-name uniqueness, per-type weight rules, and DSCP range/uniqueness. name
// is used in diagnostics. Nil queue slots (the write path fills gaps by index)
// are skipped. (QoS policies carry no cross-spec references, so shape is the
// whole of their validation.)
func (q *QoSPolicy) ValidateShape(name string) error {
	v := &util.ValidationBuilder{}
	q.validateShape(v, name)
	return v.Build()
}

// validateShape appends a QoS policy's structural errors to a shared builder —
// the form the loader uses to accumulate errors across every policy in one pass.
func (q *QoSPolicy) validateShape(v *util.ValidationBuilder, name string) {
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

// ValidateShape checks a service's type-level constraints — the references its
// service_type requires and that service_type is known. The references
// themselves (do they resolve?) are checked separately by MissingRefs; this
// covers which references each type mandates.
func (s *ServiceSpec) ValidateShape(name string) error {
	v := &util.ValidationBuilder{}
	s.validateShape(v, "", name)
	return v.Build()
}

func (s *ServiceSpec) validateShape(v *util.ValidationBuilder, prefix, name string) {
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
}

// ValidateShape checks a node spec's required fields and value formats. isHost
// relaxes the rules to a host device (only mgmt_ip required); knownZones is the
// set of zones the spec's `zone` must be one of (nil skips that check for
// callers that validate zone membership elsewhere).
func (n *NodeSpec) ValidateShape(isHost bool, knownZones map[string]*ZoneSpec) error {
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

// ValidateShapes appends the shape errors of every shape-bearing spec in the set
// to a shared builder — the QoS policies and the services. prefix labels the
// scope in messages (e.g. "zone 'amer': "). It is the load-side aggregate of the
// per-object Validate/ValidateShape methods the write path calls individually.
func (o *OverridableSpecs) ValidateShapes(v *util.ValidationBuilder, prefix string) {
	for name, policy := range o.QoSPolicies {
		policy.validateShape(v, prefix+name)
	}
	for name, svc := range o.Services {
		svc.validateShape(v, prefix, name)
	}
}
