// Package auth provides permission-based access control.
//
// Permission enforcement design:
// - Write operations are checked via checkExecutePermission() when -x (execute) flag is set
// - Read/view operations are always allowed (no permission check in dry-run/preview mode)
// - Permissions are defined in network.json under "permissions" and "super_users"
// - Service-specific permission overrides are supported via ServiceSpec.Permissions
package auth

// Permission defines an action that can be controlled
type Permission string

// Write permissions — enforced via checkExecutePermission() in CLI write commands.
const (
	PermServiceApply  Permission = "service.apply"
	PermServiceRemove Permission = "service.remove"

	PermInterfaceModify Permission = "interface.modify"

	PermLAGCreate Permission = "lag.create"
	PermLAGModify Permission = "lag.modify"
	PermLAGDelete Permission = "lag.delete"

	PermVLANCreate Permission = "vlan.create"
	PermVLANModify Permission = "vlan.modify"
	PermVLANDelete Permission = "vlan.delete"

	PermACLModify Permission = "acl.modify"

	PermEVPNModify Permission = "evpn.modify"

	PermQoSCreate Permission = "qos.create"
	PermQoSModify Permission = "qos.modify"
	PermQoSDelete Permission = "qos.delete"

	PermVRFCreate Permission = "vrf.create"
	PermVRFModify Permission = "vrf.modify"
	PermVRFDelete Permission = "vrf.delete"

	PermSpecAuthor Permission = "spec.author"

	PermFilterCreate Permission = "filter.create"
	PermFilterDelete Permission = "filter.delete"
)

// Context carries the per-decision inputs Checker.Check consumes.
//
// Caller is the username the check is being made for. The HTTP path
// (auth-design.md L3) populates it from the verified identity on the
// request context — Unix peer creds, mTLS cert CN, or PAM-verified
// username. CLI in-process callers populate it directly when they
// engage the checker.
//
// Device, Service, Interface, Resource are scoping dimensions the
// Checker reads against per-service grants and (in L5) where clauses.
type Context struct {
	Caller    string
	Device    string
	Service   string
	Interface string
	Resource  string
}

// NewContext creates a new permission context
func NewContext() *Context {
	return &Context{}
}

// WithCaller sets the username the check is being made for. Required
// for any Check call once SetAuth is wired (auth-design.md L3) —
// otherwise the check denies. The HTTP boundary populates this from
// audit.CallerFromContext via Network.checkPermission; direct in-
// process callers set it themselves.
func (c *Context) WithCaller(caller string) *Context {
	c.Caller = caller
	return c
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
