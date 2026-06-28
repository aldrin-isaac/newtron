package newtron

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// SpecBinding is one active application of a spec onto a device interface,
// recorded as a topology step (apply-service, bind-ipvpn, bind-macvpn,
// bind-qos, create-acl). Bindings are the dimension the spec-reference guards
// (checkNoConsumersAnyScope) cannot see: a spec may be referenced nowhere in
// the spec graph yet still be applied on the wire via topology steps. Deleting
// such a spec without surfacing the bindings leaves dangling steps that name a
// spec that no longer exists.
type SpecBinding struct {
	Device    string
	Interface string
}

// bindingsFor scans every device's topology steps and returns the interfaces on
// which the named spec is currently applied. It is the inverse of provenance:
// DeriveSpecRef maps a step back to the spec it actuates, so a step is a binding
// of (kind, name) exactly when DeriveSpecRef(step) returns that pair. kind is a
// DeriveSpecRef provenance kind (SpecKindService, SpecKindIPVPN, …), not a
// spec-graph kind ("ServiceSpec"). Returns nil when no topology is loaded or
// nothing binds the spec.
func (net *Network) bindingsFor(kind, name string) []SpecBinding {
	topo := net.GetTopology()
	if topo == nil {
		return nil
	}
	canonical := util.NormalizeName(name)
	var out []SpecBinding
	for device, dev := range topo.Nodes {
		if dev == nil {
			continue
		}
		for _, step := range dev.Steps {
			k, n := DeriveSpecRef(step.URL, step.Params)
			if k == kind && n == canonical {
				out = append(out, SpecBinding{Device: device, Interface: interfaceFromStepURL(step.URL)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Device != out[j].Device {
			return out[i].Device < out[j].Device
		}
		return out[i].Interface < out[j].Interface
	})
	return out
}

// interfaceFromStepURL pulls the interface name out of an interface-scoped
// topology step URL like "/interfaces/Ethernet0/apply-service" → "Ethernet0".
// Returns "" when the URL has no /interfaces/<name>/ segment — the binding is
// still reported, just identified by device alone.
func interfaceFromStepURL(url string) string {
	parts := strings.Split(strings.Trim(url, "/"), "/")
	for i, p := range parts {
		if p == "interfaces" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// guardSpecBindings refuses deletion of a spec that is still applied on one or
// more device interfaces, unless force is set. kind is the DeriveSpecRef
// provenance kind used to match topology steps; resource is the spec-graph label
// ("ServiceSpec", "IPVPNSpec", …) so the 409 a client sees carries the same
// Resource as the spec-reference and override guards for that spec.
//
// Without force it returns a *util.ConflictError (→ 409) listing every binding
// as "device:interface". With force it cascade-removes the binding steps from
// topology.json (§15 reference-aware reverse, mirroring DeleteNodeSpec's link
// cascade) so the delete leaves no dangling step. Force removes only the
// topology record; a live device keeps the applied CONFIG_DB until reconciled —
// un-apply on the device first (remove-service) to avoid drift.
func (net *Network) guardSpecBindings(kind, resource, name string, force bool) error {
	bindings := net.bindingsFor(kind, name)
	if len(bindings) == 0 {
		return nil
	}
	if !force {
		refs := make([]string, len(bindings))
		for i, b := range bindings {
			if b.Interface == "" {
				refs[i] = b.Device
				continue
			}
			refs[i] = b.Device + ":" + b.Interface
		}
		return &util.ConflictError{Resource: resource, Name: name, References: refs, Force: true}
	}
	return net.removeBindings(kind, name)
}

// removeBindings drops every topology step that binds (kind, name) and persists
// each touched device. It re-derives the match per step rather than trusting
// positions from an earlier scan, so it stays correct under the write lock even
// if steps shifted. SaveDeviceIntents replaces a device's steps wholesale and
// writes topology.json atomically.
func (net *Network) removeBindings(kind, name string) error {
	topo := net.GetTopology()
	if topo == nil {
		return nil
	}
	canonical := util.NormalizeName(name)
	for device, dev := range topo.Nodes {
		if dev == nil {
			continue
		}
		var kept []spec.TopologyStep
		removed := false
		for _, step := range dev.Steps {
			k, n := DeriveSpecRef(step.URL, step.Params)
			if k == kind && n == canonical {
				removed = true
				continue
			}
			kept = append(kept, step)
		}
		if !removed {
			continue
		}
		if err := net.SaveDeviceIntents(device, kept); err != nil {
			return fmt.Errorf("removing %s '%s' bindings from %s: %w", kind, name, device, err)
		}
	}
	return nil
}
