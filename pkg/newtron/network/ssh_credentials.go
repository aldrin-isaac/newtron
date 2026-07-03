package network

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// ssh_credentials.go — scope-aware writes and reads for the device SSH login
// (ssh_user / ssh_pass), the SCALAR analog of the map-overridable scope surface
// in scoped_writes.go / scoped_reads.go ("flat at the boundary, hierarchical
// underneath", DESIGN_PRINCIPLES_NEWTRON §7).
//
// The login is a single value per scope, not a named collection, so it gets its
// own target resolver rather than riding the (kind, name) machinery of
// withWriteTarget. Everything else is the same: network lives in network.json
// (persist), zone in zones/<name>.json (MutateZoneSpec), node in nodes/<name>.json
// (MutateNodeSpec, secret-safe), all under keyNetworkSpec.
//
// NETWORK-FLOOR INVARIANT (§7) — everything overridable has a network floor, and
// the SSH login is no exception. A zone/node login override may exist only if the
// network scope has a login authored (checkSSHOverrideBase, the scalar analog of
// checkOverrideBase), and the network base may not be emptied while any override
// sits below it (checkNoSSHOverridesBelow, the analog of checkNoOverridesBelow):
// clear bottom-up. The "base exists" predicate is whole-object — the network
// SSHCredentials is non-empty (either field set) — since the login is one
// resource, not a named collection.

// hasNetworkSSHBase reports whether the network scope authors a login (either
// field set) — the "base exists" predicate for the network-floor invariant.
// Caller holds keyNetworkSpec.
func (n *Network) hasNetworkSSHBase() bool {
	return n.spec.SSHUser != "" || n.spec.SSHPass != ""
}

// checkSSHOverrideBase enforces the network-floor invariant on a scoped set: a
// zone/node login override may exist only if the network scope authors a login.
// Network-scope writes are unconstrained. Returns *spec.ReferenceError (→ 400) —
// the override "references" a required network base that is absent. The scalar
// analog of checkOverrideBase. Caller holds keyNetworkSpec.
func (n *Network) checkSSHOverrideBase(scope string) error {
	if scope == "" || scope == spec.ScopeNetwork {
		return nil
	}
	if n.hasNetworkSSHBase() {
		return nil
	}
	return &spec.ReferenceError{Errors: []string{fmt.Sprintf(
		"ssh login has no network-scope definition; a %s-scope override requires a network base (network-floor invariant)",
		scope)}}
}

// checkNoSSHOverridesBelow refuses to empty the network-scope login while any
// zone/node override still exists — removing the base would leave those overrides
// without the network floor the invariant requires. Deletion is bottom-up: clear
// the overrides first (§15). Returns *util.ConflictError (→ 409). The scalar
// analog of checkNoOverridesBelow. Caller holds keyNetworkSpec.
func (n *Network) checkNoSSHOverridesBelow() error {
	var locs []string
	for name, z := range n.loader.Zones() {
		if z.SSHUser != "" || z.SSHPass != "" {
			locs = append(locs, "override at "+scopeLabel(spec.ScopeZone, name))
		}
	}
	for _, name := range n.ListNodeSpecs() {
		// loader.LoadNodeSpec (raw) — an unresolved ${secret:} ssh_pass still
		// counts as an authored override; loadNodeSpec would resolve it, but the
		// presence check only cares that a value is set.
		p, err := n.loader.LoadNodeSpec(name)
		if err != nil {
			return fmt.Errorf("loading node spec %q for ssh floor check: %w", name, err)
		}
		if p.SSHUser != "" || p.SSHPass != "" {
			locs = append(locs, "override at "+scopeLabel(spec.ScopeNode, name))
		}
	}
	if len(locs) == 0 {
		return nil
	}
	return &util.ConflictError{Resource: "ssh-credentials", Name: "network", References: locs}
}

// withSSHTarget resolves the SSHCredentials a scoped write targets, runs fn
// against it under the right lock, and persists — the SSH-login counterpart of
// withWriteTarget. It is the single place that knows where each scope's login
// lives and how it is locked/persisted. The network-floor checks run inside fn
// (under the lock), reading n.spec / the loader exactly as their map analogs do.
func (n *Network) withSSHTarget(scope, instance string, fn func(*spec.SSHCredentials) error) error {
	switch scope {
	case "", spec.ScopeNetwork:
		mu := n.locks.lock(keyNetworkSpec)
		mu.Lock()
		defer mu.Unlock()
		if err := fn(&n.spec.SSHCredentials); err != nil {
			return err
		}
		return n.persistSpec()
	case spec.ScopeZone:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		if _, ok := n.loader.Zone(instance); !ok {
			return &newtronErrors{notFound: true, resource: "zone", id: instance}
		}
		return n.loader.MutateZoneSpec(instance, func(z *spec.ZoneSpec) error {
			return fn(&z.SSHCredentials)
		})
	case spec.ScopeNode:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		return n.loader.MutateNodeSpec(instance, func(p *spec.NodeSpec) error {
			return fn(&p.SSHCredentials)
		})
	default:
		return &newtronErrors{notFound: true, resource: "scope", id: scope}
	}
}

// SetSSHCredentials sets (replaces) the SSH login at the given scope. Either
// field may be empty, meaning "not set at this scope" — resolveEffectiveSSH then
// inherits it from the next scope up. ssh_pass may be a ${secret:KEY} reference;
// it is stored verbatim and resolved only at read/connect (never eagerly here).
//
// Network-floor invariant (§7): a zone/node set requires a network base; emptying
// the network base (both fields empty at network scope) is refused while any
// override sits below it.
func (n *Network) SetSSHCredentials(scope, instance, sshUser, sshPass string) error {
	return n.withSSHTarget(scope, instance, func(c *spec.SSHCredentials) error {
		if scope == "" || scope == spec.ScopeNetwork {
			// Setting the network base to empty is a clear — guard it (bottom-up).
			if sshUser == "" && sshPass == "" {
				if err := n.checkNoSSHOverridesBelow(); err != nil {
					return err
				}
			}
		} else if err := n.checkSSHOverrideBase(scope); err != nil {
			return err
		}
		c.SSHUser = sshUser
		c.SSHPass = sshPass
		return nil
	})
}

// ClearSSHCredentials removes the SSH login override at the given scope (both
// fields) — the reverse of SetSSHCredentials (§15). A scoped override clear is
// always safe (consumers fall back to the network base the floor guarantees).
// Clearing the network base is refused while any override sits below it — clear
// bottom-up (checkNoSSHOverridesBelow).
func (n *Network) ClearSSHCredentials(scope, instance string) error {
	return n.withSSHTarget(scope, instance, func(c *spec.SSHCredentials) error {
		if scope == "" || scope == spec.ScopeNetwork {
			if err := n.checkNoSSHOverridesBelow(); err != nil {
				return err
			}
		}
		c.SSHUser = ""
		c.SSHPass = ""
		return nil
	})
}

// GetSSHCredentialsAt reads the login AUTHORED at exactly one scope — no
// hierarchy fallback and no secret resolution (the read mirror of
// SetSSHCredentials, so anything writable at a scope is readable at it, §24).
// The raw stored value is returned: a ${secret:KEY} ssh_pass comes back as the
// pointer it is, not resolved to plaintext, so an authoring UI can see which key
// is referenced while the caller (ShowSSHCredentials) masks any plaintext before
// it reaches the wire. A zone/node request reads only that scope's file.
func (n *Network) GetSSHCredentialsAt(scope, instance string) (spec.SSHCredentials, error) {
	switch scope {
	case "", spec.ScopeNetwork:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		return n.spec.SSHCredentials, nil
	case spec.ScopeZone:
		mu := n.locks.lock(keyNetworkSpec)
		mu.RLock()
		defer mu.RUnlock()
		z, ok := n.loader.Zone(instance)
		if !ok {
			return spec.SSHCredentials{}, &newtronErrors{notFound: true, resource: "zone", id: instance}
		}
		return z.SSHCredentials, nil
	case spec.ScopeNode:
		// loader.LoadNodeSpec (raw), NOT n.loadNodeSpec — the latter resolves the
		// node's own ${secret:} refs to plaintext, which must never reach a read.
		p, err := n.loader.LoadNodeSpec(instance)
		if err != nil {
			return spec.SSHCredentials{}, err
		}
		return p.SSHCredentials, nil
	default:
		return spec.SSHCredentials{}, &newtronErrors{notFound: true, resource: "scope", id: scope}
	}
}
