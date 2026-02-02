package operations

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// ApplyServiceOp applies a service definition to an interface.
// This operation delegates to Interface.ApplyService() in the network package.
type ApplyServiceOp struct {
	BaseOperation
	Interface   string
	ServiceName string
	IPAddress   string
	PeerAS      int // For BGP services where peer_as="request"
}

// NewApplyServiceOp creates a new service application operation.
func NewApplyServiceOp(iface, serviceName, ipAddr string) *ApplyServiceOp {
	return &ApplyServiceOp{
		Interface:   iface,
		ServiceName: serviceName,
		IPAddress:   ipAddr,
	}
}

// WithPeerAS sets the BGP peer AS number (for services with peer_as="request").
func (op *ApplyServiceOp) WithPeerAS(peerAS int) *ApplyServiceOp {
	op.PeerAS = peerAS
	return op
}

// Name returns the operation name
func (op *ApplyServiceOp) Name() string {
	return "service.apply"
}

// Description returns a human-readable description
func (op *ApplyServiceOp) Description() string {
	return fmt.Sprintf("Apply service '%s' to interface %s", op.ServiceName, op.Interface)
}

// Validate checks all preconditions for applying a service
func (op *ApplyServiceOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)
	checker.RequireInterfaceNotLAGMember(op.Interface)
	checker.RequireServiceExists(op.ServiceName)

	if d.InterfaceHasService(op.Interface) {
		checker.Check(false, "interface must have no service bound",
			fmt.Sprintf("interface %s already has a service - remove it first", op.Interface))
	}

	// Check if service requires PeerAS but it wasn't provided
	svc, err := d.Network().GetService(op.ServiceName)
	if err == nil && svc.Routing != nil {
		if svc.Routing.Protocol == "bgp" && svc.Routing.PeerAS == "request" && op.PeerAS == 0 {
			checker.Check(false, "peer AS required",
				fmt.Sprintf("service '%s' requires --peer-as parameter", op.ServiceName))
		}
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns all changes that would be made
func (op *ApplyServiceOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	// Delegate to network package's Interface.ApplyService
	intf, err := d.GetInterface(op.Interface)
	if err != nil {
		return nil, err
	}

	networkCS, err := intf.ApplyService(ctx, op.ServiceName, network.ApplyServiceOpts{
		IPAddress: op.IPAddress,
		PeerAS:    op.PeerAS,
	})
	if err != nil {
		return nil, err
	}

	// Convert network.ChangeSet to operations.ChangeSet
	cs := NewChangeSet(d.Name(), op.Name())
	for _, c := range networkCS.Changes {
		cs.AddChange(c.Table, c.Key, string(c.Type), c.OldValue, c.NewValue)
	}

	return cs, nil
}

// Execute applies all changes
func (op *ApplyServiceOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	intf, err := d.GetInterface(op.Interface)
	if err != nil {
		return err
	}

	cs, err := intf.ApplyService(ctx, op.ServiceName, network.ApplyServiceOpts{
		IPAddress: op.IPAddress,
		PeerAS:    op.PeerAS,
	})
	if err != nil {
		return err
	}

	return cs.Apply(d)
}

// RemoveServiceOp removes a service from an interface
type RemoveServiceOp struct {
	BaseOperation
	Interface string
}

// NewRemoveServiceOp creates a service removal operation.
func NewRemoveServiceOp(iface string) *RemoveServiceOp {
	return &RemoveServiceOp{
		Interface: iface,
	}
}

// Name returns the operation name
func (op *RemoveServiceOp) Name() string {
	return "service.remove"
}

// Description returns a human-readable description
func (op *RemoveServiceOp) Description() string {
	return fmt.Sprintf("Remove service from interface %s", op.Interface)
}

// Validate checks preconditions
func (op *RemoveServiceOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)

	if !d.InterfaceHasService(op.Interface) {
		checker.Check(false, "interface must have a service bound",
			fmt.Sprintf("interface %s has no service to remove", op.Interface))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes that would be made
func (op *RemoveServiceOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	// Delegate to network package's Interface.RemoveService
	intf, err := d.GetInterface(op.Interface)
	if err != nil {
		return nil, err
	}

	networkCS, err := intf.RemoveService(ctx)
	if err != nil {
		return nil, err
	}

	// Convert network.ChangeSet to operations.ChangeSet
	cs := NewChangeSet(d.Name(), op.Name())
	for _, c := range networkCS.Changes {
		cs.AddChange(c.Table, c.Key, string(c.Type), c.OldValue, c.NewValue)
	}

	return cs, nil
}

// Execute removes the service
func (op *RemoveServiceOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	intf, err := d.GetInterface(op.Interface)
	if err != nil {
		return err
	}

	cs, err := intf.RemoveService(ctx)
	if err != nil {
		return err
	}

	util.WithDevice(d.Name()).Infof("Removed service from interface %s", op.Interface)
	return cs.Apply(d)
}
