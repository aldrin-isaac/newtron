package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// ConfigureInterfaceOp configures an interface's basic settings
type ConfigureInterfaceOp struct {
	BaseOperation
	Interface   string
	Desc        string
	MTU         int
	Speed       string
	AdminStatus string
}

// Name returns the operation name
func (op *ConfigureInterfaceOp) Name() string {
	return "interface.configure"
}

// Description returns a human-readable description
func (op *ConfigureInterfaceOp) Description() string {
	return fmt.Sprintf("Configure interface %s", op.Interface)
}

// Validate checks all preconditions
func (op *ConfigureInterfaceOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	// Basic preconditions
	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)

	// Interface must not be a LAG member (configure the LAG instead)
	checker.RequireInterfaceNotLAGMember(op.Interface)

	// Validate MTU if provided
	if op.MTU > 0 {
		if err := util.ValidateMTU(op.MTU); err != nil {
			checker.Check(false, "valid MTU", err.Error())
		}
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes that would be made
func (op *ConfigureInterfaceOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	fields := make(map[string]string)
	if op.Desc != "" {
		fields["description"] = op.Desc
	}
	if op.MTU > 0 {
		fields["mtu"] = strconv.Itoa(op.MTU)
	}
	if op.Speed != "" {
		fields["speed"] = op.Speed
	}
	if op.AdminStatus != "" {
		fields["admin_status"] = op.AdminStatus
	}

	if len(fields) > 0 {
		cs.AddChange("PORT", op.Interface, ChangeModify, nil, fields)
	}

	return cs, nil
}

// Execute applies the changes
func (op *ConfigureInterfaceOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	cs, err := op.Preview(ctx, d)
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}

	if err := applyPreview(cs, d); err != nil {
		return err
	}

	util.WithDevice(d.Name()).Infof("Configured interface %s", op.Interface)
	return nil
}

// SetInterfaceVRFOp binds an interface to a VRF
type SetInterfaceVRFOp struct {
	BaseOperation
	Interface string
	VRF       string
	IPAddress string // Optional IP address to configure
}

// Name returns the operation name
func (op *SetInterfaceVRFOp) Name() string {
	return "interface.set-vrf"
}

// Description returns a human-readable description
func (op *SetInterfaceVRFOp) Description() string {
	return fmt.Sprintf("Bind interface %s to VRF %s", op.Interface, op.VRF)
}

// Validate checks all preconditions
func (op *SetInterfaceVRFOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)

	// VRF must exist before binding interface
	if op.VRF != "" && op.VRF != "default" {
		checker.RequireVRFExists(op.VRF)
	}

	// Interface must not be a LAG member
	checker.RequireInterfaceNotLAGMember(op.Interface)

	// Validate IP address if provided
	if op.IPAddress != "" {
		if !util.IsValidIPv4CIDR(op.IPAddress) {
			checker.Check(false, "valid IP address", fmt.Sprintf("invalid CIDR: %s", op.IPAddress))
		}
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *SetInterfaceVRFOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	// Add VRF binding
	cs.AddChange("INTERFACE", op.Interface, ChangeModify, nil, map[string]string{
		"vrf_name": op.VRF,
	})

	// Add IP address if specified
	if op.IPAddress != "" {
		key := fmt.Sprintf("%s|%s", op.Interface, op.IPAddress)
		cs.AddChange("INTERFACE", key, ChangeAdd, nil, map[string]string{})
	}

	return cs, nil
}

// Execute applies the changes
func (op *SetInterfaceVRFOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	cs, err := op.Preview(ctx, d)
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}

	if err := applyPreview(cs, d); err != nil {
		return err
	}

	util.WithDevice(d.Name()).Infof("Bound interface %s to VRF %s", op.Interface, op.VRF)
	return nil
}

// SetInterfaceIPOp configures an IP address on an interface
type SetInterfaceIPOp struct {
	BaseOperation
	Interface string
	IPAddress string
}

// Name returns the operation name
func (op *SetInterfaceIPOp) Name() string {
	return "interface.set-ip"
}

// Description returns a human-readable description
func (op *SetInterfaceIPOp) Description() string {
	return fmt.Sprintf("Configure IP %s on interface %s", op.IPAddress, op.Interface)
}

// Validate checks all preconditions
func (op *SetInterfaceIPOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)
	checker.RequireInterfaceNotLAGMember(op.Interface)

	// Validate IP address
	if !util.IsValidIPv4CIDR(op.IPAddress) {
		checker.Check(false, "valid IP address", fmt.Sprintf("invalid CIDR: %s", op.IPAddress))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *SetInterfaceIPOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())
	key := fmt.Sprintf("%s|%s", op.Interface, op.IPAddress)
	cs.AddChange("INTERFACE", key, ChangeAdd, nil, map[string]string{})

	return cs, nil
}

// Execute applies the changes
func (op *SetInterfaceIPOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	cs, err := op.Preview(ctx, d)
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}

	if err := applyPreview(cs, d); err != nil {
		return err
	}

	util.WithDevice(d.Name()).Infof("Configured IP %s on interface %s", op.IPAddress, op.Interface)
	return nil
}

// BindACLOp binds an ACL to an interface
type BindACLOp struct {
	BaseOperation
	Interface string
	ACLName   string
	Direction string // ingress, egress
}

// Name returns the operation name
func (op *BindACLOp) Name() string {
	return "interface.bind-acl"
}

// Description returns a human-readable description
func (op *BindACLOp) Description() string {
	return fmt.Sprintf("Bind ACL %s to interface %s (%s)", op.ACLName, op.Interface, op.Direction)
}

// Validate checks all preconditions
func (op *BindACLOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Interface = util.NormalizeInterfaceName(op.Interface)

	checker := NewPreconditionChecker(d, op.Name(), op.Interface)

	checker.RequireConnected()
	checker.RequireLocked()
	checker.RequireInterfaceExists(op.Interface)

	// ACL table must exist before binding
	checker.RequireACLTableExists(op.ACLName)

	// Validate direction
	if op.Direction != "ingress" && op.Direction != "egress" {
		checker.Check(false, "valid direction", "direction must be 'ingress' or 'egress'")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *BindACLOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())
	cs.AddChange("ACL_TABLE", op.ACLName, ChangeModify, nil, map[string]string{
		"ports": op.Interface, // Would need to merge with existing
		"stage": op.Direction,
	})

	return cs, nil
}

// Execute applies the changes
func (op *BindACLOp) Execute(ctx context.Context, d *network.Device) error {
	if err := op.RequireValidated(); err != nil {
		return err
	}

	cs, err := op.Preview(ctx, d)
	if err != nil {
		return fmt.Errorf("preview: %w", err)
	}

	if err := applyPreview(cs, d); err != nil {
		return err
	}

	util.WithDevice(d.Name()).Infof("Bound ACL %s to interface %s (%s)", op.ACLName, op.Interface, op.Direction)
	return nil
}
