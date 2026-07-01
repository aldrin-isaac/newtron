package network

import (
	"fmt"
	"sort"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// scoped_writes.go — routing and integrity for scope-aware spec writes
// ("flat at the boundary, hierarchical underneath", P2).
//
// A spec write targets a scope (network / zone / node) and an instance (the
// zone or node name; empty for network). withWriteTarget resolves the target
// OverridableSpecs, runs the per-kind closure against it under the right lock,
// and persists — so the write methods mutate that container instead of always
// n.spec. network/zone live in network.json (keyNetworkSpec); node lives in
// nodes/<name>.json (persisted via loader.MutateNodeSpec, which is secret-safe).
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
// The two delete guards both reuse the #285 reflection (FindConsumers / HasSpec)
// applied across every scope's container:
//
//   - checkOverrideBase — a scoped create/update requires the network base.
//   - checkNoConsumersAnyScope + checkNoOverridesBelow — a network delete is
//     refused while anything references it (any scope) or any override sits
//     below it.
//
// The integrity helpers read n.spec and the zone containers directly and assume
// the caller already holds keyNetworkSpec; withWriteTarget holds it (write lock
// for network/zone, read lock for node so the floor base stays stable).

// withWriteTarget resolves the OverridableSpecs a scoped write targets, runs fn
// against it under the right lock, and persists. It is the single place that
// knows where each scope lives and how it is locked/persisted:
//
//   - network → n.spec.OverridableSpecs, under keyNetworkSpec, persist network.json
//   - zone    → the zone's OverridableSpecs, under keyNetworkSpec, persist network.json
//   - node    → the nodeSpec's OverridableSpecs, persisted to nodes/<name>.json via
//     loader.MutateNodeSpec (serialized, secret-safe). The network-spec RLock is
//     held for the duration so the floor base that fn checks (checkOverrideBase)
//     can't be deleted mid-write, and so lock order stays keyNetworkSpec → loader.
//
// fn mutates the passed container and runs the per-kind checks (existence,
// checkRefsResolve, checkOverrideBase) — all of which read n.spec under the lock
// withWriteTarget holds. fn must not re-acquire keyNetworkSpec or call the loader.
func (n *Network) withWriteTarget(scope, instance string, fn func(specs *spec.OverridableSpecs) error) error {
	switch scope {
	case "", spec.ScopeNetwork:
		mu := n.locks.lock(keyNetworkSpec)
		mu.Lock()
		defer mu.Unlock()
		if err := fn(&n.spec.OverridableSpecs); err != nil {
			return err
		}
		return n.persistSpec()
	case spec.ScopeZone:
		// Localize the write to zones/<instance>.json (mirrors the node case).
		// keyNetworkSpec.RLock guards the network base MutateZoneSpec reads for
		// the network-floor re-validation; MutateZoneSpec serializes the
		// zone-file write under the loader lock.
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		// A write to an unknown zone is a clean not-found (404), symmetric with
		// the read path (withReadTarget's ScopeZone case) — not the raw
		// file-open error MutateZoneSpec would surface for a missing zone file.
		if _, ok := n.loader.Zone(instance); !ok {
			return &newtronErrors{notFound: true, resource: "zone", id: instance}
		}
		return n.loader.MutateZoneSpec(instance, func(z *spec.ZoneSpec) error {
			return fn(&z.OverridableSpecs)
		})
	case spec.ScopeNode:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		return n.loader.MutateNodeSpec(instance, func(p *spec.NodeSpec) error {
			return fn(&p.OverridableSpecs)
		})
	default:
		return &newtronErrors{notFound: true, resource: "scope", id: scope}
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
// network, each zone, each node nodeSpec — with the scope token and instance
// name. The single cross-scope walk the reverse-integrity guards build on; the
// two callers are read-only (FindConsumers / HasSpec), so ranging the loader's
// zone snapshot is safe. Caller holds keyNetworkSpec for the network base;
// zones and nodeSpecs load via the loader's own lock. A load failure is a
// fail-closed error, never a silent skip.
func (n *Network) eachScopeContainer(fn func(scope, instance string, specs *spec.OverridableSpecs) error) error {
	if err := fn(spec.ScopeNetwork, "", &n.spec.OverridableSpecs); err != nil {
		return err
	}
	for name, z := range n.loader.Zones() {
		if err := fn(spec.ScopeZone, name, &z.OverridableSpecs); err != nil {
			return err
		}
	}
	for _, name := range n.ListNodeSpecs() {
		nodeSpec, err := n.GetNodeSpec(name)
		if err != nil {
			return fmt.Errorf("loading node spec %q for cross-scope integrity check: %w", name, err)
		}
		if err := fn(spec.ScopeNode, name, &nodeSpec.OverridableSpecs); err != nil {
			return err
		}
	}
	return nil
}

// checkNoConsumersAnyScope refuses to delete (kind, name) while any spec at any
// scope references it — the cross-scope reverse-dependency guard for every spec
// delete (§15), needed once scoped consumers can reference a network base. The
// reference graph is read generically from the `ref:`/`kind:` tags
// (OverridableSpecs.FindConsumers). Returns *util.ConflictError (→ HTTP 409).
// Caller holds keyNetworkSpec.
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

// containedOverrides lists the spec overrides held in a scope container (a zone
// or a node nodeSpec), as sorted "KindSpec 'NAME'" strings. A zone/nodeSpec that
// still holds overrides must not be deleted out from under them — deleting the
// container would silently remove authored scoped resources. The reverse-delete
// guard for the containers themselves (§15), symmetric with checkNoOverridesBelow
// for the network base.
func containedOverrides(specs *spec.OverridableSpecs) []string {
	var out []string
	specs.EachSpec(func(kind, name string, _ any) {
		out = append(out, fmt.Sprintf("%s '%s'", kind, name))
	})
	sort.Strings(out)
	return out
}
