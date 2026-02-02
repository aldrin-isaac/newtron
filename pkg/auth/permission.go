// Package auth provides permission-based access control.
package auth

// Permission defines an action that can be controlled
type Permission string

// Standard permissions
const (
	PermServiceApply  Permission = "service.apply"
	PermServiceRemove Permission = "service.remove"
	PermServiceView   Permission = "service.view"

	PermInterfaceConfig Permission = "interface.configure"
	PermInterfaceModify Permission = "interface.modify"
	PermInterfaceView   Permission = "interface.view"

	PermLAGCreate Permission = "lag.create"
	PermLAGModify Permission = "lag.modify"
	PermLAGDelete Permission = "lag.delete"
	PermLAGView   Permission = "lag.view"

	PermVLANCreate Permission = "vlan.create"
	PermVLANModify Permission = "vlan.modify"
	PermVLANDelete Permission = "vlan.delete"
	PermVLANView   Permission = "vlan.view"

	PermACLCreate Permission = "acl.create"
	PermACLModify Permission = "acl.modify"
	PermACLDelete Permission = "acl.delete"
	PermACLView   Permission = "acl.view"

	PermEVPNModify Permission = "evpn.modify"
	PermEVPNView   Permission = "evpn.view"

	PermBGPModify Permission = "bgp.modify"
	PermBGPView   Permission = "bgp.view"

	PermQoSModify Permission = "qos.modify"
	PermQoSView   Permission = "qos.view"

	PermBaselineApply Permission = "baseline.apply"
	PermHealthCheck   Permission = "health.check"

	PermDeviceConnect    Permission = "device.connect"
	PermDeviceLock       Permission = "device.lock"
	PermDeviceDisconnect Permission = "device.disconnect"

	PermAuditView Permission = "audit.view"

	// v3: Port and BGP configuration permissions
	PermPortCreate   Permission = "port.create"
	PermPortDelete   Permission = "port.delete"
	PermBGPConfigure Permission = "bgp.configure"

	// v4: Composite delivery and topology provisioning permissions
	PermCompositeDeliver  Permission = "composite.deliver"
	PermTopologyProvision Permission = "topology.provision"

	PermAll Permission = "all" // Superuser - allows everything
)

// PermissionCategory groups related permissions
type PermissionCategory struct {
	Name        string
	Description string
	Permissions []Permission
}

// StandardCategories defines standard permission categories
var StandardCategories = []PermissionCategory{
	{
		Name:        "service",
		Description: "Service management",
		Permissions: []Permission{PermServiceApply, PermServiceRemove, PermServiceView},
	},
	{
		Name:        "interface",
		Description: "Interface configuration",
		Permissions: []Permission{PermInterfaceConfig, PermInterfaceModify, PermInterfaceView},
	},
	{
		Name:        "lag",
		Description: "Link aggregation",
		Permissions: []Permission{PermLAGCreate, PermLAGModify, PermLAGDelete, PermLAGView},
	},
	{
		Name:        "vlan",
		Description: "VLAN management",
		Permissions: []Permission{PermVLANCreate, PermVLANModify, PermVLANDelete, PermVLANView},
	},
	{
		Name:        "acl",
		Description: "Access control lists",
		Permissions: []Permission{PermACLCreate, PermACLModify, PermACLDelete, PermACLView},
	},
	{
		Name:        "evpn",
		Description: "EVPN/VXLAN",
		Permissions: []Permission{PermEVPNModify, PermEVPNView},
	},
	{
		Name:        "bgp",
		Description: "BGP routing",
		Permissions: []Permission{PermBGPModify, PermBGPView},
	},
	{
		Name:        "qos",
		Description: "Quality of Service",
		Permissions: []Permission{PermQoSModify, PermQoSView},
	},
	{
		Name:        "baseline",
		Description: "Baseline configuration",
		Permissions: []Permission{PermBaselineApply},
	},
	{
		Name:        "health",
		Description: "Health checks",
		Permissions: []Permission{PermHealthCheck},
	},
	{
		Name:        "device",
		Description: "Device connection",
		Permissions: []Permission{PermDeviceConnect, PermDeviceLock, PermDeviceDisconnect},
	},
	{
		Name:        "audit",
		Description: "Audit log access",
		Permissions: []Permission{PermAuditView},
	},
	{
		Name:        "port",
		Description: "Port creation and deletion",
		Permissions: []Permission{PermPortCreate, PermPortDelete},
	},
	{
		Name:        "composite",
		Description: "Composite config delivery",
		Permissions: []Permission{PermCompositeDeliver},
	},
	{
		Name:        "topology",
		Description: "Topology provisioning",
		Permissions: []Permission{PermTopologyProvision},
	},
}

// Context provides context for permission checks
type Context struct {
	Device    string
	Service   string
	Interface string
	Resource  string
}

// NewContext creates a new permission context
func NewContext() *Context {
	return &Context{}
}

// WithDevice sets the device context
func (c *Context) WithDevice(device string) *Context {
	c.Device = device
	return c
}

// WithService sets the service context
func (c *Context) WithService(service string) *Context {
	c.Service = service
	return c
}

// WithInterface sets the interface context
func (c *Context) WithInterface(iface string) *Context {
	c.Interface = iface
	return c
}

// WithResource sets a generic resource context
func (c *Context) WithResource(resource string) *Context {
	c.Resource = resource
	return c
}

// IsReadOnly returns true if the permission is read-only
func (p Permission) IsReadOnly() bool {
	switch p {
	case PermServiceView, PermInterfaceView, PermLAGView, PermVLANView,
		PermACLView, PermEVPNView, PermBGPView, PermQoSView, PermAuditView,
		PermHealthCheck:
		return true
	}
	return false
}

// IsWriteOperation returns true if the permission involves modification
func (p Permission) IsWriteOperation() bool {
	return !p.IsReadOnly() && p != PermDeviceConnect && p != PermDeviceDisconnect
}

// RequiresLock returns true if the permission requires device lock
func (p Permission) RequiresLock() bool {
	return p.IsWriteOperation()
}
