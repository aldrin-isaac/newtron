package newtron

import (
	"context"

	"github.com/aldrin-isaac/newtron/pkg/newtron/auth"
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Interface is a scoped interface context within a Node.
// The interface is the point of service delivery — where abstract
// service intent meets physical infrastructure.
type Interface struct {
	node     *Node
	internal *node.Interface
}

// gate consults the parent Network's permission checker (auth-design.md
// L4) before an Interface mutation acts. Populates auth.Context.Device,
// Interface, and Resource so the audit decision event records the full
// dimensional context for L5 to later constrain. When the resource
// argument is empty (e.g., ConfigureInterface has no per-call resource
// beyond the interface itself), Resource stays empty.
//
// For service-bound operations (ApplyService/RemoveService/RefreshService)
// use gateService — it also populates Context.Service so
// ServiceSpec.Permissions overrides and `where: {service: ...}` grant
// clauses can match (auth-design.md §L3 + §L5).
func (i *Interface) gate(ctx context.Context, perm auth.Permission, resource string) error {
	return i.node.net.checkPermission(ctx, perm, auth.NewContext().
		WithDevice(i.node.internal.Name()).
		WithInterface(i.internal.Name()).
		WithResource(resource))
}

// gateService is the gate helper for service-bound Interface
// operations. In addition to Device/Interface/Resource, it stamps
// Context.Service with the service name — the L3 per-service-override
// path in Checker.checkUser (checker.go:44) gates on Context.Service
// being non-empty, and L5 `where: {service: "<pattern>"}` clauses
// match against the same field. Without this dimension populated, both
// mechanisms are unreachable from production HTTP gates.
//
// svcName is also stamped on Resource so existing grants written
// against `where: {resource: "<svc>"}` keep matching — a pure
// addition of the Service dimension, not a relocation.
func (i *Interface) gateService(ctx context.Context, perm auth.Permission, svcName string) error {
	return i.node.net.checkPermission(ctx, perm, auth.NewContext().
		WithDevice(i.node.internal.Name()).
		WithInterface(i.internal.Name()).
		WithService(svcName).
		WithResource(svcName))
}

// ============================================================================
// Write Operations
// ============================================================================

// ApplyService applies a service definition to this interface.
//
// Normalization-then-gate ordering: the canonical form of the service
// name is what network.json grants are written against (CLAUDE.md
// "Normalize at the Boundary"), so the gate must see the normalized
// name. Otherwise a `where: {service: "TRANSIT"}` clause would miss a
// caller request for "transit" — both are the same service, but the
// raw form doesn't match the canonical grant pattern.
func (i *Interface) ApplyService(ctx context.Context, service string, opts ApplyServiceOpts) error {
	service = util.NormalizeName(service)
	if err := i.gateService(ctx, auth.PermServiceApply, service); err != nil {
		return err
	}
	cs, err := i.internal.ApplyService(ctx, service, opts.internal())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveService removes the service from this interface. Recovers the
// bound service name from the on-device binding (Interface.ServiceName,
// interface.go:188) and stamps it on Context.Service so L5
// `where: {service: ...}` clauses scope this reverse op the same way
// they scope ApplyService — operational symmetry at the gate level
// (DPN §15).
func (i *Interface) RemoveService(ctx context.Context) error {
	svcName := i.internal.ServiceName()
	if err := i.gateService(ctx, auth.PermServiceRemove, svcName); err != nil {
		return err
	}
	cs, err := i.internal.RemoveService(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RefreshService reapplies the service configuration to sync with the
// service definition. Recovers the bound service name the same way
// RemoveService does — refresh acts on the currently-applied service,
// not a caller-named one.
func (i *Interface) RefreshService(ctx context.Context) error {
	svcName := i.internal.ServiceName()
	if err := i.gateService(ctx, auth.PermServiceApply, svcName); err != nil {
		return err
	}
	cs, err := i.internal.RefreshService(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// BindACL binds an ACL to this interface.
func (i *Interface) BindACL(ctx context.Context, acl, direction string) error {
	if err := i.gate(ctx, auth.PermACLModify, acl); err != nil {
		return err
	}
	cs, err := i.internal.BindACL(ctx, acl, direction)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// UnbindACL removes this interface from an ACL table's binding list.
// Delegates to the Node-level UnbindACLFromInterface method, which resolves
// the interface name internally.
func (i *Interface) UnbindACL(ctx context.Context, acl string) error {
	if err := i.gate(ctx, auth.PermACLModify, acl); err != nil {
		return err
	}
	cs, err := i.node.internal.UnbindACLFromInterface(ctx, acl, i.internal.Name())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// AddBGPPeer adds a direct BGP peer on this interface.
func (i *Interface) AddBGPPeer(ctx context.Context, config BGPNeighborConfig) error {
	if err := i.gate(ctx, auth.PermBGPPeer, config.NeighborIP); err != nil {
		return err
	}
	cs, err := i.internal.AddBGPPeer(ctx, config.directPeer())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// UpdateBGPPeer atomically mutates fields on the existing BGP peer on
// this interface. Per §47 the key (vrf, neighbor_ip) is immutable;
// to change the neighbor IP, remove and re-add. §15 mirror of AddBGPPeer
// that closes the session-blip window remove + add exposes today (#227).
func (i *Interface) UpdateBGPPeer(ctx context.Context, config BGPNeighborConfig) error {
	peerIP := i.internal.DirectBGPPeerIP()
	if err := i.gate(ctx, auth.PermBGPPeer, peerIP); err != nil {
		return err
	}
	cs, err := i.internal.UpdateBGPPeer(ctx, config.directPeer())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveBGPPeer removes a direct BGP peer from this interface.
// The neighbor IP is recovered from the intent record before gating
// so `where: {resource: "<peer-ip>"}` clauses scope this reverse op
// symmetrically with AddBGPPeer (DPN §15; #163). If no peer is
// bound, gateResource sees Resource="" and the internal call
// returns the "no BGP peer intent" error after authorization runs.
func (i *Interface) RemoveBGPPeer(ctx context.Context) error {
	peerIP := i.internal.DirectBGPPeerIP()
	if err := i.gate(ctx, auth.PermBGPPeer, peerIP); err != nil {
		return err
	}
	cs, err := i.internal.RemoveBGPPeer(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveTrunkVLAN removes one VLAN from this interface's trunk membership.
// Reverse mirror of ConfigureInterface(tagged=true) per §15 — closes the
// gap where unconfigure-interface was the only removal path and tore
// down everything on the port (#224).
func (i *Interface) RemoveTrunkVLAN(ctx context.Context, vlanID int) error {
	if err := i.gate(ctx, auth.PermInterfaceModify, ""); err != nil {
		return err
	}
	cs, err := i.internal.RemoveTrunkVLAN(ctx, vlanID)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// ConfigureInterface sets forwarding mode on an interface. Routed mode (VRF+IP)
// and bridged mode (VLAN membership) are mutually exclusive.
func (i *Interface) ConfigureInterface(ctx context.Context, cfg InterfaceConfig) error {
	if err := i.gate(ctx, auth.PermInterfaceModify, ""); err != nil {
		return err
	}
	cs, err := i.internal.ConfigureInterface(ctx, cfg.internal())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// UnconfigureInterface reverses ConfigureInterface. Reads the intent record to
// determine what was configured, then undoes it. No parameters needed.
func (i *Interface) UnconfigureInterface(ctx context.Context) error {
	if err := i.gate(ctx, auth.PermInterfaceModify, ""); err != nil {
		return err
	}
	cs, err := i.internal.UnconfigureInterface(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// SetProperty sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description.
func (i *Interface) SetProperty(ctx context.Context, property, value string) error {
	if err := i.gate(ctx, auth.PermInterfaceModify, property); err != nil {
		return err
	}
	cs, err := i.internal.SetProperty(ctx, property, value)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// ClearProperty removes a property override from this interface,
// reverting the field to its default and deleting the property intent.
func (i *Interface) ClearProperty(ctx context.Context, property string) error {
	if err := i.gate(ctx, auth.PermInterfaceModify, property); err != nil {
		return err
	}
	cs, err := i.internal.ClearProperty(ctx, property)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// BindQoS binds a QoS policy to this interface.
// Resolves the QoS policy spec by name from the node's SpecProvider.
func (i *Interface) BindQoS(ctx context.Context, policy string) error {
	if err := i.gate(ctx, auth.PermQoSModify, policy); err != nil {
		return err
	}
	cs, err := i.internal.BindQoS(ctx, util.NormalizeName(policy))
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// UnbindQoS unbinds the QoS policy from this interface. Recovers the
// bound policy name from the intent record before gating so
// `where: {resource: "<policy>"}` clauses scope this reverse op
// symmetrically with BindQoS (DPN §15; #163).
func (i *Interface) UnbindQoS(ctx context.Context) error {
	policy := i.internal.QoSPolicyName()
	if err := i.gate(ctx, auth.PermQoSModify, policy); err != nil {
		return err
	}
	cs, err := i.internal.UnbindQoS(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}
