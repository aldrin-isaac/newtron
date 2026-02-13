// Package operations provides precondition checking utilities for device configuration.
package operations

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// PreconditionChecker helps build precondition checks.
// It uses *network.Device which provides access to both device state
// and network-level configuration through parent references.
type PreconditionChecker struct {
	device    *network.Device
	operation string
	resource  string
	errors    []error
}

// NewPreconditionChecker creates a new precondition checker
func NewPreconditionChecker(d *network.Device, operation, resource string) *PreconditionChecker {
	return &PreconditionChecker{
		device:    d,
		operation: operation,
		resource:  resource,
	}
}

// RequireConnected checks that the device is connected
func (p *PreconditionChecker) RequireConnected() *PreconditionChecker {
	if !p.device.IsConnected() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "device must be connected", ""))
	}
	return p
}

// RequireLocked checks that the device is locked
func (p *PreconditionChecker) RequireLocked() *PreconditionChecker {
	if !p.device.IsLocked() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "device must be locked for changes", "use Lock() first"))
	}
	return p
}

// RequireInterfaceExists checks that an interface exists
func (p *PreconditionChecker) RequireInterfaceExists(name string) *PreconditionChecker {
	if !p.device.InterfaceExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must exist", fmt.Sprintf("interface '%s' not found", name)))
	}
	return p
}

// RequireInterfaceNotExists checks that an interface does not exist
func (p *PreconditionChecker) RequireInterfaceNotExists(name string) *PreconditionChecker {
	if p.device.InterfaceExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must not exist", fmt.Sprintf("interface '%s' already exists", name)))
	}
	return p
}

// RequireInterfaceNotLAGMember checks that an interface is not a LAG member
func (p *PreconditionChecker) RequireInterfaceNotLAGMember(name string) *PreconditionChecker {
	if p.device.InterfaceIsLAGMember(name) {
		lag := p.device.GetInterfaceLAG(name)
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must not be a LAG member",
			fmt.Sprintf("interface '%s' is member of %s", name, lag)))
	}
	return p
}

// RequireInterfaceIsLAGMember checks that an interface is a LAG member
func (p *PreconditionChecker) RequireInterfaceIsLAGMember(name, lag string) *PreconditionChecker {
	actualLAG := p.device.GetInterfaceLAG(name)
	if actualLAG != lag {
		if actualLAG == "" {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource, "interface must be a LAG member",
				fmt.Sprintf("interface '%s' is not a member of %s", name, lag)))
		} else {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource, "interface is member of wrong LAG",
				fmt.Sprintf("interface '%s' is member of %s, not %s", name, actualLAG, lag)))
		}
	}
	return p
}

// RequireInterfaceNoService checks that no service is bound to the interface
func (p *PreconditionChecker) RequireInterfaceNoService(name string) *PreconditionChecker {
	if p.device.InterfaceHasService(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "interface must have no service bound",
			fmt.Sprintf("interface '%s' has a service bound - remove it first", name)))
	}
	return p
}

// RequireVLANExists checks that a VLAN exists
func (p *PreconditionChecker) RequireVLANExists(id int) *PreconditionChecker {
	if !p.device.VLANExists(id) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VLAN must exist",
			fmt.Sprintf("VLAN %d not found - create it first", id)))
	}
	return p
}

// RequireVLANNotExists checks that a VLAN does not exist
func (p *PreconditionChecker) RequireVLANNotExists(id int) *PreconditionChecker {
	if p.device.VLANExists(id) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VLAN must not exist",
			fmt.Sprintf("VLAN %d already exists", id)))
	}
	return p
}

// RequireVRFExists checks that a VRF exists
func (p *PreconditionChecker) RequireVRFExists(name string) *PreconditionChecker {
	if !p.device.VRFExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VRF must exist",
			fmt.Sprintf("VRF '%s' not found - create it first", name)))
	}
	return p
}

// RequireVRFNotExists checks that a VRF does not exist
func (p *PreconditionChecker) RequireVRFNotExists(name string) *PreconditionChecker {
	if p.device.VRFExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VRF must not exist",
			fmt.Sprintf("VRF '%s' already exists", name)))
	}
	return p
}

// RequirePortChannelExists checks that a PortChannel exists
func (p *PreconditionChecker) RequirePortChannelExists(name string) *PreconditionChecker {
	if !p.device.PortChannelExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must exist",
			fmt.Sprintf("PortChannel '%s' not found - create it first", name)))
	}
	return p
}

// RequirePortChannelNotExists checks that a PortChannel does not exist
func (p *PreconditionChecker) RequirePortChannelNotExists(name string) *PreconditionChecker {
	if p.device.PortChannelExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "PortChannel must not exist",
			fmt.Sprintf("PortChannel '%s' already exists", name)))
	}
	return p
}

// RequireVTEPConfigured checks that VTEP is configured (for EVPN)
func (p *PreconditionChecker) RequireVTEPConfigured() *PreconditionChecker {
	if !p.device.VTEPExists() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "VTEP must be configured",
			"EVPN requires VTEP - configure baseline first"))
	}
	return p
}

// RequireBGPConfigured checks that BGP is configured
func (p *PreconditionChecker) RequireBGPConfigured() *PreconditionChecker {
	if !p.device.BGPConfigured() {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "BGP must be configured",
			"EVPN requires BGP - configure baseline first"))
	}
	return p
}

// RequireACLTableExists checks that an ACL table exists
func (p *PreconditionChecker) RequireACLTableExists(name string) *PreconditionChecker {
	if !p.device.ACLTableExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "ACL table must exist",
			fmt.Sprintf("ACL table '%s' not found - create it first", name)))
	}
	return p
}

// RequireACLTableNotExists checks that an ACL table does not exist
func (p *PreconditionChecker) RequireACLTableNotExists(name string) *PreconditionChecker {
	if p.device.ACLTableExists(name) {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "ACL table must not exist",
			fmt.Sprintf("ACL table '%s' already exists", name)))
	}
	return p
}

// RequirePortAllowed checks that port creation is allowed by validating the port
// name against the device's platform.json (if loaded).
func (p *PreconditionChecker) RequirePortAllowed(portName string) *PreconditionChecker {
	underlying := p.device.Underlying()
	if underlying.PlatformConfig != nil {
		if _, ok := underlying.PlatformConfig.Interfaces[portName]; !ok {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource, "port must be defined in platform.json",
				fmt.Sprintf("port '%s' not found in device platform config", portName)))
		}
	}
	return p
}

// RequirePlatformLoaded checks that the device's platform.json has been loaded.
func (p *PreconditionChecker) RequirePlatformLoaded() *PreconditionChecker {
	underlying := p.device.Underlying()
	if underlying.PlatformConfig == nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "platform config must be loaded",
			"call LoadPlatformConfig() first"))
	}
	return p
}

// RequireNoExistingService checks that no service is bound to an interface.
// Used by composite merge to ensure no conflicts.
func (p *PreconditionChecker) RequireNoExistingService(interfaceName string) *PreconditionChecker {
	configDB := p.device.ConfigDB()
	if configDB != nil {
		if binding, ok := configDB.NewtronServiceBinding[interfaceName]; ok {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource, "interface must not have existing service",
				fmt.Sprintf("interface '%s' has service '%s' bound — remove it first", interfaceName, binding.ServiceName)))
		}
	}
	return p
}

// RequirePeerGroupExists checks that a BGP peer group exists.
func (p *PreconditionChecker) RequirePeerGroupExists(name string) *PreconditionChecker {
	configDB := p.device.ConfigDB()
	if configDB != nil {
		if _, ok := configDB.BGPPeerGroup[name]; !ok {
			p.errors = append(p.errors, util.NewPreconditionError(
				p.operation, p.resource, "peer group must exist",
				fmt.Sprintf("BGP peer group '%s' not found — create it first", name)))
		}
	}
	return p
}

// RequireServiceExists checks that a service definition exists in network config
func (p *PreconditionChecker) RequireServiceExists(name string) *PreconditionChecker {
	_, err := p.device.Network().GetService(name)
	if err != nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "service must exist",
			fmt.Sprintf("service '%s' not found in network config", name)))
	}
	return p
}

// RequireFilterSpecExists checks that a filter spec exists in network config
func (p *PreconditionChecker) RequireFilterSpecExists(name string) *PreconditionChecker {
	_, err := p.device.Network().GetFilterSpec(name)
	if err != nil {
		p.errors = append(p.errors, util.NewPreconditionError(
			p.operation, p.resource, "filter spec must exist",
			fmt.Sprintf("filter spec '%s' not found in network config", name)))
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

// ============================================================================
// DependencyChecker - Reverse dependency checking for safe deletion
// ============================================================================

// DependencyChecker checks if resources can be safely deleted by verifying
// no other resources depend on them. This is the reverse of PreconditionChecker.
//
// NOTE: There is also a DependencyChecker in pkg/network/interface_ops.go
// which is used directly by Interface.RemoveService(). This version in
// operations is for use by standalone Operation implementations.
// Consider consolidating if import cycles can be avoided.
type DependencyChecker struct {
	device           *network.Device
	excludeInterface string // Interface being removed (excluded from counts)
}

// NewDependencyChecker creates a new dependency checker
func NewDependencyChecker(d *network.Device, excludeInterface string) *DependencyChecker {
	return &DependencyChecker{
		device:           d,
		excludeInterface: excludeInterface,
	}
}

// IsLastACLUser returns true if this is the last interface using the ACL
func (dc *DependencyChecker) IsLastACLUser(aclName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return true // ACL doesn't exist, safe to "delete"
	}

	// Count interfaces excluding the one being removed
	count := 0
	for _, intf := range splitPorts(acl.Ports) {
		if intf != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastVLANMember returns true if this is the last member of the VLAN
func (dc *DependencyChecker) IsLastVLANMember(vlanID int) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	vlanName := fmt.Sprintf("Vlan%d", vlanID)

	// Count members excluding the one being removed
	count := 0
	for key := range configDB.VLANMember {
		// Key format: Vlan100|Ethernet0
		if len(key) > len(vlanName)+1 && key[:len(vlanName)+1] == vlanName+"|" {
			memberIface := key[len(vlanName)+1:]
			if memberIface != dc.excludeInterface {
				count++
			}
		}
	}
	return count == 0
}

// IsLastVRFUser returns true if this is the last interface bound to the VRF
func (dc *DependencyChecker) IsLastVRFUser(vrfName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	// Count interfaces bound to this VRF
	count := 0
	for intfName, intf := range configDB.Interface {
		// Skip composite keys (with |) - those are IP bindings
		if strings.Contains(intfName, "|") {
			continue
		}
		if intf.VRFName == vrfName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// IsLastServiceUser returns true if this is the last interface using the service
func (dc *DependencyChecker) IsLastServiceUser(serviceName string) bool {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return true
	}

	// Count service bindings for this service
	count := 0
	for intfName, binding := range configDB.NewtronServiceBinding {
		if binding.ServiceName == serviceName && intfName != dc.excludeInterface {
			count++
		}
	}
	return count == 0
}

// GetACLRemainingInterfaces returns the interfaces that will remain after removing the excluded one
func (dc *DependencyChecker) GetACLRemainingInterfaces(aclName string) string {
	configDB := dc.device.ConfigDB()
	if configDB == nil {
		return ""
	}

	acl, ok := configDB.ACLTable[aclName]
	if !ok {
		return ""
	}

	var remaining []string
	for _, intf := range splitPorts(acl.Ports) {
		if intf != dc.excludeInterface {
			remaining = append(remaining, intf)
		}
	}
	return strings.Join(remaining, ",")
}

// splitPorts splits a comma-separated ports string into a slice
func splitPorts(ports string) []string {
	if ports == "" {
		return nil
	}
	var result []string
	for _, p := range strings.Split(ports, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
