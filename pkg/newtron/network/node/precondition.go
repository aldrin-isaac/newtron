package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/util"
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
func (n *Node) precondition(operation, resource string) *PreconditionChecker {
	return NewPreconditionChecker(n, operation, resource).
		RequireConnected().
		RequireLocked()
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

// RequireVLANExists checks that a VLAN exists
func (p *PreconditionChecker) RequireVLANExists(id int) *PreconditionChecker {
	if !p.node.VLANExists(id) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VLAN must exist",
			fmt.Sprintf("VLAN %d not found - create it first", id)))
	}
	return p
}

// RequireVLANNotExists checks that a VLAN does not exist
func (p *PreconditionChecker) RequireVLANNotExists(id int) *PreconditionChecker {
	if p.node.VLANExists(id) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VLAN must not exist",
			fmt.Sprintf("VLAN %d already exists", id)))
	}
	return p
}

// RequireVRFExists checks that a VRF exists
func (p *PreconditionChecker) RequireVRFExists(name string) *PreconditionChecker {
	if !p.node.VRFExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VRF must exist",
			fmt.Sprintf("VRF '%s' not found - create it first", name)))
	}
	return p
}

// RequireVRFNotExists checks that a VRF does not exist
func (p *PreconditionChecker) RequireVRFNotExists(name string) *PreconditionChecker {
	if p.node.VRFExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VRF must not exist",
			fmt.Sprintf("VRF '%s' already exists", name)))
	}
	return p
}

// RequirePortChannelExists checks that a PortChannel exists
func (p *PreconditionChecker) RequirePortChannelExists(name string) *PreconditionChecker {
	if !p.node.PortChannelExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must exist",
			fmt.Sprintf("PortChannel '%s' not found - create it first", name)))
	}
	return p
}

// RequirePortChannelNotExists checks that a PortChannel does not exist
func (p *PreconditionChecker) RequirePortChannelNotExists(name string) *PreconditionChecker {
	if p.node.PortChannelExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must not exist",
			fmt.Sprintf("PortChannel '%s' already exists", name)))
	}
	return p
}

// RequireVTEPConfigured checks that VTEP is configured (for EVPN)
func (p *PreconditionChecker) RequireVTEPConfigured() *PreconditionChecker {
	if !p.node.VTEPExists() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VTEP must be configured",
			"EVPN requires VTEP - configure baseline first"))
	}
	return p
}

// RequireACLTableExists checks that an ACL table exists
func (p *PreconditionChecker) RequireACLTableExists(name string) *PreconditionChecker {
	if !p.node.ACLTableExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "ACL table must exist",
			fmt.Sprintf("ACL table '%s' not found - create it first", name)))
	}
	return p
}

// RequireACLTableNotExists checks that an ACL table does not exist
func (p *PreconditionChecker) RequireACLTableNotExists(name string) *PreconditionChecker {
	if p.node.ACLTableExists(name) {
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

// Errors returns all errors
func (p *PreconditionChecker) Errors() []error {
	return p.errors
}

// HasErrors returns true if there are any errors
func (p *PreconditionChecker) HasErrors() bool {
	return len(p.errors) > 0
}
