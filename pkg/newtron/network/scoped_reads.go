package network

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// scoped_reads.go — the READ mirror of scoped_writes.go. A scope-aware spec
// read addresses the same network / zone / node OverridableSpecs containers a
// scope-aware write does ("flat at the boundary, hierarchical underneath", P2),
// so the read and write surfaces stay symmetric: anything writable at a scope is
// readable at that scope.
//
// Unlike per-node resolution (ResolvedSpecs), a scoped read does NOT fall back
// to the network base. A zone/node request reads ONLY that scope's container, so
// the caller sees the override's own stored definition — exactly what a
// subsequent scoped write would replace — and can tell an override apart from
// the floor. If the requested scope has no entry for the name, that is a
// NotFoundError (→ 404), never a silent base fallback. Network base stays the
// default when no scope is given.

// withReadTarget resolves the OverridableSpecs a scoped read addresses and runs
// fn against it under a read lock — the read counterpart of withWriteTarget,
// knowing where each scope lives and how it is locked:
//
//   - network → n.spec.OverridableSpecs, under keyNetworkSpec (RLock)
//   - zone    → the zone's OverridableSpecs, under keyNetworkSpec (RLock)
//   - node    → the nodeSpec's OverridableSpecs, read via the loader's own lock
//     (a separate nodes/<name>.json; no keyNetworkSpec needed — the read touches
//     no n.spec state)
//
// A missing zone/node instance, or an unknown scope token, is a notFound error.
func (n *Network) withReadTarget(scope, instance string, fn func(specs *spec.OverridableSpecs) error) error {
	switch scope {
	case "", spec.ScopeNetwork:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		return fn(&n.spec.OverridableSpecs)
	case spec.ScopeZone:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		z, ok := n.spec.Zones[instance]
		if !ok {
			return &newtronErrors{notFound: true, resource: "zone", id: instance}
		}
		return fn(&z.OverridableSpecs)
	case spec.ScopeNode:
		nodeSpec, err := n.GetNodeSpec(instance)
		if err != nil {
			return err
		}
		return fn(&nodeSpec.OverridableSpecs)
	default:
		return &newtronErrors{notFound: true, resource: "scope", id: scope}
	}
}

// getSpecAt reads one spec value of kind from the container at (scope, instance).
// pick selects the per-kind map from the resolved OverridableSpecs. The name is
// looked up only in that scope's container (no base fallback) — absent ⇒
// spec.NotFoundError. It is the scope-aware generalization of getSpec; the
// network-base GetX methods delegate here with an empty scope.
func getSpecAt[V any](n *Network, scope, instance, kind, name string, pick func(*spec.OverridableSpecs) map[string]V) (V, error) {
	var out V
	err := n.withReadTarget(scope, instance, func(s *spec.OverridableSpecs) error {
		v, ok := pick(s)[util.NormalizeName(name)]
		if !ok {
			return &spec.NotFoundError{Kind: kind, Name: util.NormalizeName(name)}
		}
		out = v
		return nil
	})
	return out, err
}
