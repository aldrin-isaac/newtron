// resolved_specs.go provides the spec lookups of a node's SpecView: the seven
// overridable spec maps merged network → zone → node, lower level wins.
//
// Built by Network.ResolveNodeSpecs at the start of every operation
// (DESIGN_PRINCIPLES_NEWTRON §7). The merge is complete — every name the node can see
// at any level is in it — so a miss means the spec does not exist for this node.
package network

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Compile-time check that ResolvedSpecs satisfies node.SpecProvider.
var _ node.SpecProvider = (*ResolvedSpecs)(nil)

// ResolvedSpecs holds the merged spec maps for a single device after
// hierarchical resolution (network > zone > nodeSpec). It implements
// node.SpecProvider. It is never written after it is built, so it needs no lock.
type ResolvedSpecs struct {
	merged  spec.OverridableSpecs
	network *Network // for GetPlatform() only — platforms don't participate in hierarchy
}

// newResolvedSpecs creates a ResolvedSpecs from pre-merged maps.
func newResolvedSpecs(merged spec.OverridableSpecs, network *Network) *ResolvedSpecs {
	return &ResolvedSpecs{
		merged:  merged,
		network: network,
	}
}

// lookupResolved returns the merged definition of name. It looks the name up
// exactly as a network-level read does (getSpecAt): canonicalized first, and a
// miss is the same *spec.NotFoundError — replay relies on that type to
// recognize an orphaned intent.
func lookupResolved[V any](m map[string]V, kind, name string) (V, error) {
	name = util.NormalizeName(name)
	if v, ok := m[name]; ok {
		return v, nil
	}
	var zero V
	return zero, &spec.NotFoundError{Kind: kind, Name: name}
}

func (r *ResolvedSpecs) GetService(name string) (*spec.ServiceSpec, error) {
	return lookupResolved(r.merged.Services, "service", name)
}

func (r *ResolvedSpecs) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	return lookupResolved(r.merged.IPVPNs, "ipvpn", name)
}

func (r *ResolvedSpecs) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	return lookupResolved(r.merged.MACVPNs, "macvpn", name)
}

func (r *ResolvedSpecs) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	return lookupResolved(r.merged.QoSPolicies, "QoS policy", name)
}

func (r *ResolvedSpecs) GetFilter(name string) (*spec.FilterSpec, error) {
	return lookupResolved(r.merged.Filters, "filter", name)
}

func (r *ResolvedSpecs) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	return lookupResolved(r.merged.RoutePolicies, "route policy", name)
}

func (r *ResolvedSpecs) GetPrefixList(name string) ([]string, error) {
	return lookupResolved(r.merged.PrefixLists, "prefix list", name)
}

func (r *ResolvedSpecs) GetPlatform(name string) (*spec.PlatformSpec, error) {
	return r.network.GetPlatform(name)
}

func (r *ResolvedSpecs) FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec) {
	for name, def := range r.merged.MACVPNs {
		if def.VNI == vni {
			return name, def
		}
	}
	return "", nil
}
