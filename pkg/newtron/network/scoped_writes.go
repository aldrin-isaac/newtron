package network

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// scoped_writes.go — routing and integrity for scope-aware spec writes
// ("flat at the boundary, hierarchical underneath", P2).
//
// A spec write targets a scope (network / zone / node) and an instance (the
// zone or node name; empty for network). writeContainer resolves the target
// OverridableSpecs; the per-kind write methods mutate that container instead of
// always n.spec.
//
// Integrity follows the NETWORK-FLOOR invariant (DESIGN_PRINCIPLES_NEWTRON §7):
// a resource may exist at zone/node scope only if it also exists at network
// scope. Because every referenceable name therefore exists at network, two
// things hold:
//
//   - Forward checking is unchanged — a reference resolves iff it resolves at
//     network (checkRefsResolve, network-only, as before). Zone/node are pure
//     overrides of a guaranteed base.
//   - Resolution never dangles from any node's perspective — the network layer
//     is a floor. So deleting an override is always safe (consumers fall back to
//     the base); only deleting the network base needs guarding.
//
// The two new guards both reuse the #285 reflection (FindConsumers / HasSpec)
// applied across every scope's container:
//
//   - checkOverrideBase — a scoped create/update requires the network base.
//   - checkNoConsumersAnyScope + checkNoOverridesBelow — a network delete is
//     refused while anything references it (any scope) or any override sits
//     below it.
//
// All helpers here assume the caller already holds keyNetworkSpec (they read
// n.spec and the zone/profile containers directly, never re-locking).
//
// Node scope is not yet wired through writeContainer — it lands in P2b together
// with the profile-file persist path and its locking. The public layer rejects
// node-scope writes until then.

// writeContainer resolves the OverridableSpecs a scoped write targets. scope is
// "" (network), "network", or "zone"; instance is the zone name for zone scope.
// The caller must hold keyNetworkSpec. Network and zone both persist via
// persistSpec (network.json), so callers persist uniformly after mutating.
func (n *Network) writeContainer(scope, instance string) (*spec.OverridableSpecs, error) {
	switch scope {
	case "", spec.ScopeNetwork:
		return &n.spec.OverridableSpecs, nil
	case spec.ScopeZone:
		z, ok := n.spec.Zones[instance]
		if !ok {
			return nil, &newtronErrors{notFound: true, resource: "zone", id: instance}
		}
		return &z.OverridableSpecs, nil
	default:
		// node scope (P2b) and unknown scopes are rejected at the public layer;
		// this is the defensive internal backstop.
		return nil, &newtronErrors{notFound: true, resource: "scope", id: scope}
	}
}

// checkOverrideBase enforces the network-floor invariant on a scoped
// create/update: a zone/node override of (kind, name) may exist only if a
// network-scope definition already does. Network-scope writes are unconstrained.
// Returns *spec.ReferenceError (→ HTTP 400) — the override "references" a
// required network base that is absent. Caller holds keyNetworkSpec.
func (n *Network) checkOverrideBase(scope, kind, name string) error {
	if scope == "" || scope == spec.ScopeNetwork {
		return nil
	}
	if n.spec.HasSpec(kind, util.NormalizeName(name)) {
		return nil
	}
	return &spec.ReferenceError{Errors: []string{fmt.Sprintf(
		"%s '%s' has no network-scope definition; a %s-scope override requires a network base (network-floor invariant)",
		kind, util.NormalizeName(name), scope)}}
}

// eachScopeContainer invokes fn for every scope's OverridableSpecs container —
// network, each zone, each node profile — with the scope token and instance
// name. The single cross-scope walk the reverse-integrity guards build on.
// Caller holds keyNetworkSpec (n.spec and zones are read directly; profiles load
// via the loader's own lock). A profile that fails to load is a fail-closed
// error, never a silent skip.
func (n *Network) eachScopeContainer(fn func(scope, instance string, specs *spec.OverridableSpecs) error) error {
	if err := fn(spec.ScopeNetwork, "", &n.spec.OverridableSpecs); err != nil {
		return err
	}
	for name, z := range n.spec.Zones {
		if err := fn(spec.ScopeZone, name, &z.OverridableSpecs); err != nil {
			return err
		}
	}
	for _, name := range n.ListProfiles() {
		profile, err := n.GetProfile(name)
		if err != nil {
			return fmt.Errorf("loading profile %q for cross-scope integrity check: %w", name, err)
		}
		if err := fn(spec.ScopeNode, name, &profile.OverridableSpecs); err != nil {
			return err
		}
	}
	return nil
}

// checkNoConsumersAnyScope refuses to delete (kind, name) while any spec at any
// scope references it. It is the cross-scope generalization of checkNoConsumers,
// needed once scoped consumers can reference a network base. Returns
// *util.ConflictError (→ HTTP 409). Caller holds keyNetworkSpec.
func (n *Network) checkNoConsumersAnyScope(kind, name string) error {
	canonical := util.NormalizeName(name)
	var refs []string
	if err := n.eachScopeContainer(func(scope, instance string, specs *spec.OverridableSpecs) error {
		for _, c := range specs.FindConsumers(kind, canonical) {
			refs = append(refs, fmt.Sprintf("%s '%s' (%s) at %s", c.Kind, c.Name, c.Field, scopeLabel(scope, instance)))
		}
		return nil
	}); err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	return &util.ConflictError{Resource: kind, Name: canonical, References: refs}
}

// checkNoOverridesBelow refuses to delete a network-scope (kind, name) while any
// zone/node override of it still exists — removing the base would leave those
// overrides without the network floor the invariant requires. Deletion is
// bottom-up: remove the overrides first (§15). Returns *util.ConflictError
// (→ HTTP 409). Caller holds keyNetworkSpec.
func (n *Network) checkNoOverridesBelow(kind, name string) error {
	canonical := util.NormalizeName(name)
	var locs []string
	if err := n.eachScopeContainer(func(scope, instance string, specs *spec.OverridableSpecs) error {
		if scope == spec.ScopeNetwork {
			return nil
		}
		if specs.HasSpec(kind, canonical) {
			locs = append(locs, "override at "+scopeLabel(scope, instance))
		}
		return nil
	}); err != nil {
		return err
	}
	if len(locs) == 0 {
		return nil
	}
	return &util.ConflictError{Resource: kind, Name: canonical, References: locs}
}

// scopeLabel renders a scope + instance for diagnostics: "network",
// "zone 'amer'", "node 'leaf1'".
func scopeLabel(scope, instance string) string {
	if instance == "" {
		return scope
	}
	return scope + " '" + instance + "'"
}
