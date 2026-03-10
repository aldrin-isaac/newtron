// resolved_specs.go provides a per-device SpecProvider that holds the merged
// result of hierarchical spec resolution (network → zone → node).
//
// Built at Node creation time in resolveProfile(). All 7 overridable spec
// maps are merged with lower-level-wins semantics: node > zone > network.
package network

import (
	"sync"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// Compile-time check that ResolvedSpecs satisfies node.SpecProvider.
var _ node.SpecProvider = (*ResolvedSpecs)(nil)

// ResolvedSpecs holds the merged spec maps for a single device after
// hierarchical resolution (network > zone > profile). It implements
// node.SpecProvider so it can be passed directly to node.New().
type ResolvedSpecs struct {
	merged  spec.OverridableSpecs
	network *Network // for GetPlatform() only — platforms don't participate in hierarchy
	mu      sync.RWMutex
}

// newResolvedSpecs creates a ResolvedSpecs from pre-merged maps.
func newResolvedSpecs(merged spec.OverridableSpecs, network *Network) *ResolvedSpecs {
	return &ResolvedSpecs{
		merged:  merged,
		network: network,
	}
}

func (r *ResolvedSpecs) GetService(name string) (*spec.ServiceSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.Services[name]; ok {
		return v, nil
	}
	// Fall through to network-level specs for dynamically added entries
	// (SaveService writes to n.spec.Services after merge was built).
	return r.network.GetService(name)
}

func (r *ResolvedSpecs) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.IPVPNs[name]; ok {
		return v, nil
	}
	return r.network.GetIPVPN(name)
}

func (r *ResolvedSpecs) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.MACVPNs[name]; ok {
		return v, nil
	}
	return r.network.GetMACVPN(name)
}

func (r *ResolvedSpecs) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.QoSPolicies[name]; ok {
		return v, nil
	}
	return r.network.GetQoSPolicy(name)
}

func (r *ResolvedSpecs) GetFilter(name string) (*spec.FilterSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.Filters[name]; ok {
		return v, nil
	}
	return r.network.GetFilter(name)
}

func (r *ResolvedSpecs) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.RoutePolicies[name]; ok {
		return v, nil
	}
	return r.network.GetRoutePolicy(name)
}

func (r *ResolvedSpecs) GetPrefixList(name string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.merged.PrefixLists[name]; ok {
		return v, nil
	}
	return r.network.GetPrefixList(name)
}

func (r *ResolvedSpecs) GetPlatform(name string) (*spec.PlatformSpec, error) {
	return r.network.GetPlatform(name)
}

func (r *ResolvedSpecs) FindMACVPNByVNI(vni int) (string, *spec.MACVPNSpec) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, def := range r.merged.MACVPNs {
		if def.VNI == vni {
			return name, def
		}
	}
	// Fall through to network-level specs for dynamically added entries
	// (SaveMACVPN writes to n.spec.MACVPNs after merge was built).
	return r.network.FindMACVPNByVNI(vni)
}
