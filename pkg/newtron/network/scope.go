package network

import (
	"fmt"
	"sort"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// scope.go — cross-scope spec enumeration ("flat at the boundary, hierarchical
// underneath").
//
// newtron stores specs hierarchically (network → zone → node;
// DESIGN_PRINCIPLES_NEWTRON §7) — the same kind may be defined at any scope,
// with node overriding zone overriding network. ListScopedSpecs flattens that
// hierarchy into one inventory, tagging each definition with the scope and
// instance it lives at. It is the read surface that lets a schema-driven
// consumer present one flat spec list filtered by scope/instance, instead of
// replicating each kind's schema once per scope. Storage and resolution stay
// hierarchical; only this read surface is flattened.
//
// Enumeration reuses the single declarative point — OverridableSpecs.EachSpec
// (references.go) — once per container, so adding a spec kind needs no change
// here.

// ScopedSpec locates a single spec definition within the hierarchy: its kind
// and (canonical) name, plus the scope and instance it is defined at. Instance
// is the zone or node name; it is empty for the network scope.
type ScopedSpec struct {
	Scope    string // spec.ScopeNetwork | spec.ScopeZone | spec.ScopeNode
	Instance string // zone or node name; empty for ScopeNetwork
	Kind     string // spec kind, e.g. "ServiceSpec"
	Name     string // canonical spec name
}

// ListScopedSpecs returns every spec defined anywhere in the hierarchy —
// network, every zone, and every node nodeSpec — each tagged with the scope and
// instance it lives at. The result is sorted (scope, instance, kind, name) for
// a stable wire order.
//
// It does not resolve overrides: a name defined at both network and a node
// appears as two entries. It reports where each definition lives, not which one
// a given node applies after the node > zone > network merge.
func (n *Network) ListScopedSpecs() ([]ScopedSpec, error) {
	var out []ScopedSpec
	collect := func(scope, instance string) func(kind, name string, _ any) {
		return func(kind, name string, _ any) {
			out = append(out, ScopedSpec{Scope: scope, Instance: instance, Kind: kind, Name: name})
		}
	}

	// Network + zones share the network-spec lock; reach their embedded
	// OverridableSpecs directly (not via GetZone) so we lock once and avoid
	// re-entrant RLock.
	func() {
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()

		n.spec.EachSpec(collect(spec.ScopeNetwork, ""))
		for name, z := range n.spec.Zones {
			z.EachSpec(collect(spec.ScopeZone, name))
		}
	}()

	// Node nodeSpecs load independently of the network-spec lock. A nodeSpec that
	// fails to load is a fail-closed error (a malformed node spec must not be
	// silently dropped from the inventory) rather than a silent skip.
	for _, name := range n.ListNodeSpecs() {
		nodeSpec, err := n.GetNodeSpec(name)
		if err != nil {
			return nil, fmt.Errorf("loading node spec %q for spec inventory: %w", name, err)
		}
		nodeSpec.EachSpec(collect(spec.ScopeNode, name))
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Scope != b.Scope:
			return a.Scope < b.Scope
		case a.Instance != b.Instance:
			return a.Instance < b.Instance
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		default:
			return a.Name < b.Name
		}
	})
	return out, nil
}
