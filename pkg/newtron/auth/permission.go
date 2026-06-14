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

	PermACLCreate Permission = "acl.create"
	PermACLModify Permission = "acl.modify"
	PermACLDelete Permission = "acl.delete"

	// EVPN modifications were a single coarse permission until #164
	// split it along Resource semantics: EVPN BGP peers stamp the
	// neighbor IP, MACVPN binds stamp a VLAN identifier. One key per
	// Resource semantic eliminates the §13 overload where
	// `where: {resource: ...}` matched indiscriminately across
	// fundamentally different objects.
	PermEVPNPeer   Permission = "evpn.peer"   // AddBGPEVPNPeer/RemoveBGPEVPNPeer; Resource = peer IP
	PermEVPNMACVPN Permission = "evpn.macvpn" // BindMACVPN/UnbindMACVPN; Resource = VLAN<id>

	// PermDeviceWrite is the catch-all for operational Node-level
	// mutations whose verb is not a create/modify/delete on a
	// specific domain noun: SetupDevice, ConfigReload, RestartService,
	// ExecCommand, SaveConfig, Reconcile. Operators who want to
	// restrict these specifically grant `device.write`; the verb-
	// specific permissions don't apply because the action is a
	// device-state operation rather than a config-table mutation.
	// (auth-design.md L4)
	PermDeviceWrite Permission = "device.write"

	PermQoSCreate Permission = "qos.create"
	PermQoSModify Permission = "qos.modify"
	PermQoSDelete Permission = "qos.delete"

	PermVRFCreate Permission = "vrf.create"
	PermVRFDelete Permission = "vrf.delete"

	// VRF modifications were a single coarse permission until #164
	// split it along Resource semantics. `vrf.bind` stamps the VRF
	// name; `vrf.route` stamps the VRF name; `bgp.peer` stamps the
	// peer IP. Splitting them lets a grant `where: {resource: ...}`
	// match unambiguously — a VRF name and a peer IP cannot collide
	// in the same lookup.
	PermVRFBind  Permission = "vrf.bind"  // BindIPVPN/UnbindIPVPN; Resource = VRF name
	PermVRFRoute Permission = "vrf.route" // AddStaticRoute/RemoveStaticRoute; Resource = VRF name
	PermBGPPeer  Permission = "bgp.peer"  // Interface AddBGPPeer/RemoveBGPPeer; Resource = peer IP

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
// Device, Service, Interface, Resource, Field are scoping dimensions
// the Checker reads against per-service grants and L5 where clauses.
// Field carries the top-level network.json field name the mutation
// touches — the meta-authorization dimension that lets spec.author
// scope away from the permissions/user_groups/super_users fields
// (auth-design.md §3 criterion 9).
type Context struct {
	Caller    string
	Device    string
	Service   string
	Interface string
	Resource  string
	Field     string
}

// NewContext creates a new permission context
func NewContext() *Context {
	return &Context{}
}

// WithCaller sets the username the check is being made for. Required
// for any Check call once Network.EnableAuthorization has been called
// (auth-design.md L3) — otherwise the check denies. The HTTP boundary
// populates this from audit.CallerFromContext via
// Network.checkPermission; direct in-process callers set it themselves.
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

// WithField sets the meta-authorization dimension (auth-design.md L5
// "Meta-Authorization: Who Can Grant Access"). The field is the
// top-level network.json field name the mutation touches — services,
// permissions, user_groups, super_users, profiles, topology. A where
// clause like {"field": "!permissions,!user_groups,!super_users"}
// scopes spec.author to "services and topology, but not grants."
func (c *Context) WithField(field string) *Context {
	c.Field = field
	return c
}
