package node

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// PreconditionChecker helps build precondition checks.
// It uses *Node which provides access to both device state
// and network-level configuration through parent references.
type PreconditionChecker struct {
	node    *Node
	operation string
	resource  string
	errors    []error
}

// NewPreconditionChecker creates a new precondition checker
func NewPreconditionChecker(d *Node, operation, resource string) *PreconditionChecker {
	return &PreconditionChecker{
		node:    d,
		operation: operation,
		resource:  resource,
	}
}

// precondition returns a PreconditionChecker with RequireConnected + RequireLocked
// already called, since every write op needs both. This replaces requireWritable(d).
// In offline mode, connected/locked checks are skipped — the projection serves
// as the precondition state.
//
// Interface-scoped forward ops are additionally capability-gated here: when
// the op's registry entry declares Needs, the target interface's kind is
// checked against the capability matrix before any op logic runs (§13:
// prevent, don't detect). The lookup is by forward wire verb, so reverse
// ops never match — their exemption is structural, not a special case
// (§15: you must always be able to undo). Content-derived ops
// (contentDerivedOps) declare nil Needs and gate in-method.
func (n *Node) precondition(operation, resource string) *PreconditionChecker {
	pc := NewPreconditionChecker(n, operation, resource)
	if n.actuatedIntent {
		pc.RequireConnected().RequireLocked()
	}
	if spec, ok := opRegistry[operation]; ok && spec.Scope == ScopeInterface && len(spec.Needs) > 0 {
		pc.RequireInterfaceCapabilities(resource, spec.Needs...)
	}
	return pc
}

// RequireInterfaceCapabilities checks that the named interface's kind
// provides every listed capability — the per-kind operation gate. Two
// refusal forms: the kind lacks the capability outright ("a VLAN interface
// (IRB) does not support QoS binding"), or the kind provides it but a
// different operation owns its authoring — then the refusal redirects to
// the designed path instead of denying the capability's existence (the one
// case today: routed config on an IRB is authored via configure-irb).
func (p *PreconditionChecker) RequireInterfaceCapabilities(name string, caps ...InterfaceCapability) *PreconditionChecker {
	kind := interfaceKindOf(util.NormalizeInterfaceName(name))
	for _, c := range caps {
		if owner := authoringOwner(kind, c); owner != "" {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource,
				fmt.Sprintf("operation must own %s authoring for a %s", c, kind),
				fmt.Sprintf("%s on a %s is authored via %s", c, kind, owner)))
			continue
		}
		if !kind.HasCapability(c) {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource,
				fmt.Sprintf("interface must support %s", c),
				fmt.Sprintf("a %s does not support %s", kind, c)))
		}
	}
	return p
}

// RequireConnected checks that the device is connected
func (p *PreconditionChecker) RequireConnected() *PreconditionChecker {
	if !p.node.IsConnected() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "device must be connected", ""))
	}
	return p
}

// RequireLocked checks that the device is locked
func (p *PreconditionChecker) RequireLocked() *PreconditionChecker {
	if !p.node.IsLocked() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "device must be locked for changes", "use Lock() first"))
	}
	return p
}

// RequireInterfaceExists checks that an interface exists
func (p *PreconditionChecker) RequireInterfaceExists(name string) *PreconditionChecker {
	if !p.node.InterfaceExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must exist", fmt.Sprintf("interface '%s' not found", name)))
	}
	return p
}

// RequireInterfaceNotPortChannelMember checks that an interface is not a PortChannel member
func (p *PreconditionChecker) RequireInterfaceNotPortChannelMember(name string) *PreconditionChecker {
	if p.node.InterfaceIsPortChannelMember(name) {
		pc := p.node.GetInterfacePortChannel(name)
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must not be a PortChannel member",
			fmt.Sprintf("interface '%s' is member of %s", name, pc)))
	}
	return p
}

// RequireVLANExists checks that a VLAN exists by checking the intent DB.
func (p *PreconditionChecker) RequireVLANExists(id int) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("vlan|%d", id)) == nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VLAN must exist",
			fmt.Sprintf("VLAN %d not found - create it first", id)))
	}
	return p
}

// RequireVRFExists checks that a VRF exists by checking the intent DB.
func (p *PreconditionChecker) RequireVRFExists(name string) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("vrf|%s", name)) == nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VRF must exist",
			fmt.Sprintf("VRF '%s' not found - create it first", name)))
	}
	return p
}

// RequirePortChannelExists checks that a PortChannel exists by checking the intent DB.
func (p *PreconditionChecker) RequirePortChannelExists(name string) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("portchannel|%s", name)) == nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must exist",
			fmt.Sprintf("PortChannel '%s' not found - create it first", name)))
	}
	return p
}

// RequirePortChannelNotExists checks that a PortChannel does not exist by checking the intent DB.
func (p *PreconditionChecker) RequirePortChannelNotExists(name string) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("portchannel|%s", name)) != nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must not exist",
			fmt.Sprintf("PortChannel '%s' already exists", name)))
	}
	return p
}

// RequireVTEPConfigured checks that VTEP is configured (for EVPN) by checking
// the device intent's source_ip param. SetupDevice with source_ip always calls
// SetupVXLAN + ConfigureBGPOverlay, so the param is a reliable proxy for "VTEP is configured."
func (p *PreconditionChecker) RequireVTEPConfigured() *PreconditionChecker {
	intent := p.node.GetIntent("device")
	if intent == nil || intent.Params["source_ip"] == "" {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VTEP must be configured",
			fmt.Sprintf("no VTEP found on %s — run 'newtron -D %s evpn setup' first", p.node.Name(), p.node.Name())))
	}
	return p
}

// RequireACLTableExists checks that an ACL table exists by checking the intent DB.
func (p *PreconditionChecker) RequireACLTableExists(name string) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("acl|%s", name)) == nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "ACL table must exist",
			fmt.Sprintf("ACL table '%s' not found - create it first", name)))
	}
	return p
}

// RequireACLTableNotExists checks that an ACL table does not exist by checking the intent DB.
func (p *PreconditionChecker) RequireACLTableNotExists(name string) *PreconditionChecker {
	if p.node.GetIntent(fmt.Sprintf("acl|%s", name)) != nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "ACL table must not exist",
			fmt.Sprintf("ACL table '%s' already exists", name)))
	}
	return p
}

// Check runs a custom check
func (p *PreconditionChecker) Check(condition bool, precondition, details string) *PreconditionChecker {
	if !condition {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, precondition, details))
	}
	return p
}

// Result returns the first error or nil if all checks passed
func (p *PreconditionChecker) Result() error {
	if len(p.errors) == 0 {
		return nil
	}
	if len(p.errors) == 1 {
		return p.errors[0]
	}
	// Combine errors
	msgs := make([]string, len(p.errors))
	for i, e := range p.errors {
		msgs[i] = e.Error()
	}
	return util.NewValidationError(msgs...)
}

