// resolved_specs.go provides a per-device SpecProvider that holds the merged
// result of hierarchical spec resolution (network → zone → node).
//
// Built at Node creation time in resolveProfile(). All 8 overridable spec
// maps are merged with lower-level-wins semantics: node > zone > network.
package network

import (
	"fmt"
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
	v, ok := r.merged.Services[name]
	if !ok {
		return nil, fmt.Errorf("service '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetIPVPN(name string) (*spec.IPVPNSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.IPVPNs[name]
	if !ok {
		return nil, fmt.Errorf("ipvpn '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetMACVPN(name string) (*spec.MACVPNSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.MACVPNs[name]
	if !ok {
		return nil, fmt.Errorf("macvpn '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetQoSPolicy(name string) (*spec.QoSPolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.QoSPolicies[name]
	if !ok {
		return nil, fmt.Errorf("QoS policy '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetQoSProfile(name string) (*spec.QoSProfile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.QoSProfiles[name]
	if !ok {
		return nil, fmt.Errorf("QoS profile '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetFilter(name string) (*spec.FilterSpec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.Filters[name]
	if !ok {
		return nil, fmt.Errorf("filter '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetRoutePolicy(name string) (*spec.RoutePolicy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.RoutePolicies[name]
	if !ok {
		return nil, fmt.Errorf("route policy '%s' not found", name)
	}
	return v, nil
}

func (r *ResolvedSpecs) GetPrefixList(name string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.merged.PrefixLists[name]
	if !ok {
		return nil, fmt.Errorf("prefix list '%s' not found", name)
	}
	return v, nil
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
	return "", nil
}
