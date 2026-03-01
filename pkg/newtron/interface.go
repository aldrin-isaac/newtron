package newtron

import (
	"context"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/network/node"
)

// Interface is a scoped interface context within a Node.
// The interface is the point of service delivery — where abstract
// service intent meets physical infrastructure.
type Interface struct {
	node     *Node
	internal *node.Interface
}

// ============================================================================
// Read Accessors (1:1 delegation to node.Interface)
// ============================================================================

// Name returns the interface name (e.g., "Ethernet0", "PortChannel100").
func (i *Interface) Name() string {
	return i.internal.Name()
}

// AdminStatus returns the administrative status (up/down).
func (i *Interface) AdminStatus() string {
	return i.internal.AdminStatus()
}

// OperStatus returns the operational status (up/down).
func (i *Interface) OperStatus() string {
	return i.internal.OperStatus()
}

// Speed returns the interface speed.
func (i *Interface) Speed() string {
	return i.internal.Speed()
}

// MTU returns the interface MTU.
func (i *Interface) MTU() int {
	return i.internal.MTU()
}

// IPAddresses returns the IP addresses configured on this interface.
func (i *Interface) IPAddresses() []string {
	return i.internal.IPAddresses()
}

// VRF returns the VRF this interface is bound to.
func (i *Interface) VRF() string {
	return i.internal.VRF()
}

// ServiceName returns the name of the service bound to this interface.
func (i *Interface) ServiceName() string {
	return i.internal.ServiceName()
}

// HasService returns true if a service is bound to this interface.
func (i *Interface) HasService() bool {
	return i.internal.HasService()
}

// Description returns the interface description.
func (i *Interface) Description() string {
	return i.internal.Description()
}

// IngressACL returns the name of the ingress ACL bound to this interface.
func (i *Interface) IngressACL() string {
	return i.internal.IngressACL()
}

// EgressACL returns the name of the egress ACL bound to this interface.
func (i *Interface) EgressACL() string {
	return i.internal.EgressACL()
}

// IsPortChannelMember returns true if this interface is a PortChannel member.
func (i *Interface) IsPortChannelMember() bool {
	return i.internal.IsPortChannelMember()
}

// PortChannelParent returns the name of the parent PortChannel (if this is a member).
func (i *Interface) PortChannelParent() string {
	return i.internal.PortChannelParent()
}

// PortChannelMembers returns the member interfaces if this is a PortChannel.
func (i *Interface) PortChannelMembers() []string {
	return i.internal.PortChannelMembers()
}

// VLANMembers returns the member interfaces if this is a VLAN interface.
func (i *Interface) VLANMembers() []string {
	return i.internal.VLANMembers()
}

// IsPortChannel returns true if this is a PortChannel interface.
func (i *Interface) IsPortChannel() bool {
	return strings.HasPrefix(i.internal.Name(), "PortChannel")
}

// IsVLAN returns true if this is a VLAN interface.
func (i *Interface) IsVLAN() bool {
	return strings.HasPrefix(i.internal.Name(), "Vlan")
}

// BGPNeighbors returns BGP neighbors configured on this interface.
func (i *Interface) BGPNeighbors() []string {
	return i.internal.BGPNeighbors()
}

// String returns the interface name as a string representation.
func (i *Interface) String() string {
	return i.internal.Name()
}

// ============================================================================
// Write Operations
// ============================================================================

// ApplyService applies a service definition to this interface.
func (i *Interface) ApplyService(ctx context.Context, service string, opts ApplyServiceOpts) error {
	cs, err := i.internal.ApplyService(ctx, service, node.ApplyServiceOpts{
		IPAddress: opts.IPAddress,
		PeerAS:    opts.PeerAS,
	})
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveService removes the service from this interface.
func (i *Interface) RemoveService(ctx context.Context) error {
	cs, err := i.internal.RemoveService(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RefreshService reapplies the service configuration to sync with the service definition.
func (i *Interface) RefreshService(ctx context.Context) error {
	cs, err := i.internal.RefreshService(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// SetIP configures an IP address on this interface.
func (i *Interface) SetIP(ctx context.Context, ip string) error {
	cs, err := i.internal.SetIP(ctx, ip)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveIP removes an IP address from this interface.
func (i *Interface) RemoveIP(ctx context.Context, ip string) error {
	cs, err := i.internal.RemoveIP(ctx, ip)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// SetVRF binds this interface to a VRF.
func (i *Interface) SetVRF(ctx context.Context, vrf string) error {
	cs, err := i.internal.SetVRF(ctx, vrf)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// BindACL binds an ACL to this interface.
func (i *Interface) BindACL(ctx context.Context, acl, direction string) error {
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
	cs, err := i.node.internal.UnbindACLFromInterface(ctx, acl, i.internal.Name())
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// BindMACVPN binds this VLAN interface to a MAC-VPN definition.
func (i *Interface) BindMACVPN(ctx context.Context, macvpnName string) error {
	macvpnDef, err := i.node.internal.GetMACVPN(macvpnName)
	if err != nil {
		return err
	}
	cs, err := i.internal.BindMACVPN(ctx, macvpnName, macvpnDef)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// UnbindMACVPN removes the MAC-VPN binding from this VLAN interface.
func (i *Interface) UnbindMACVPN(ctx context.Context) error {
	cs, err := i.internal.UnbindMACVPN(ctx)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// AddBGPNeighbor adds a direct BGP neighbor on this interface.
func (i *Interface) AddBGPNeighbor(ctx context.Context, config BGPNeighborConfig) error {
	cs, err := i.internal.AddBGPNeighbor(ctx, node.DirectBGPNeighborConfig{
		NeighborIP:  config.NeighborIP,
		RemoteAS:    config.RemoteAS,
		Description: config.Description,
	})
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// RemoveBGPNeighbor removes a direct BGP neighbor from this interface.
func (i *Interface) RemoveBGPNeighbor(ctx context.Context, ip string) error {
	cs, err := i.internal.RemoveBGPNeighbor(ctx, ip)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}

// Set sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description.
func (i *Interface) Set(ctx context.Context, property, value string) error {
	cs, err := i.internal.Set(ctx, property, value)
	if err != nil {
		return err
	}
	i.node.appendPending(cs)
	return nil
}
