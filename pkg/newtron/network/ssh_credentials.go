package network

import (
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
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
// NO NETWORK-FLOOR INVARIANT. Unlike a map override — which requires a network
// base so a reference never dangles (§7) — the SSH login's resolution is already
// total without any scope setting it: resolveEffectiveSSH falls back to the
// platform Credentials, then "admin". So a zone/node override is self-sufficient
// and needs no network base beneath it, and every clear is safe (nothing is ever
// left dangling). That is why there is no checkOverrideBase / checkNoOverridesBelow
// analog here.

// withSSHTarget resolves the SSHCredentials a scoped write targets, runs fn
// against it under the right lock, and persists — the SSH-login counterpart of
// withWriteTarget. It is the single place that knows where each scope's login
// lives and how it is locked/persisted.
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
func (n *Network) SetSSHCredentials(scope, instance, sshUser, sshPass string) error {
	return n.withSSHTarget(scope, instance, func(c *spec.SSHCredentials) error {
		c.SSHUser = sshUser
		c.SSHPass = sshPass
		return nil
	})
}

// ClearSSHCredentials removes the SSH login override at the given scope (both
// fields) — the reverse of SetSSHCredentials (§15). Always safe: no override is
// ever left dangling because resolution falls back through the hierarchy.
func (n *Network) ClearSSHCredentials(scope, instance string) error {
	return n.withSSHTarget(scope, instance, func(c *spec.SSHCredentials) error {
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
