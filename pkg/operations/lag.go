package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// CreateLAGOp creates a new Link Aggregation Group
type CreateLAGOp struct {
	BaseOperation
	LAGName  string
	Members  []string
	MinLinks int
	Mode     string // active, passive, on
	FastRate bool
	MTU      int
}

// Name returns the operation name
func (op *CreateLAGOp) Name() string {
	return "lag.create"
}

// Description returns a human-readable description
func (op *CreateLAGOp) Description() string {
	return fmt.Sprintf("Create LAG %s with members %v", op.LAGName, op.Members)
}

// Validate checks all preconditions
func (op *CreateLAGOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface names (e.g., Po100 -> PortChannel100, Eth0 -> Ethernet0)
	op.LAGName = util.NormalizeInterfaceName(op.LAGName)
	for i, member := range op.Members {
		op.Members[i] = util.NormalizeInterfaceName(member)
	}

	checker := NewPreconditionChecker(d, op.Name(), op.LAGName)

	checker.RequireConnected()
	checker.RequireLocked()

	// LAG must not already exist
	checker.RequirePortChannelNotExists(op.LAGName)

	// All member interfaces must exist
	for _, member := range op.Members {
		checker.RequireInterfaceExists(member)

		// Member must not already be in another LAG
		checker.RequireInterfaceNotLAGMember(member)

		// Member must not have a service bound (would be lost)
		checker.RequireInterfaceNoService(member)
	}

	// Need at least one member
	checker.Check(len(op.Members) > 0, "at least one member", "LAG requires at least one member interface")

	// Validate mode
	validModes := map[string]bool{"active": true, "passive": true, "on": true}
	if op.Mode != "" && !validModes[op.Mode] {
		checker.Check(false, "valid LACP mode", "mode must be 'active', 'passive', or 'on'")
	}

	// Validate MTU
	if op.MTU > 0 {
		if err := util.ValidateMTU(op.MTU); err != nil {
			checker.Check(false, "valid MTU", err.Error())
		}
	}

	// Validate min_links
	if op.MinLinks > len(op.Members) {
		checker.Check(false, "valid min_links", fmt.Sprintf("min_links (%d) cannot exceed member count (%d)", op.MinLinks, len(op.Members)))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *CreateLAGOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	// Create PortChannel entry
	mtu := op.MTU
	if mtu == 0 {
		mtu = 9100
	}
	minLinks := op.MinLinks
	if minLinks == 0 {
		minLinks = 1
	}
	mode := op.Mode
	if mode == "" {
		mode = "active"
	}

	pcFields := map[string]string{
		"admin_status": "up",
		"mtu":          strconv.Itoa(mtu),
		"min_links":    strconv.Itoa(minLinks),
	}
	if op.FastRate {
		pcFields["fast_rate"] = "true"
	}

	cs.AddChange("PORTCHANNEL", op.LAGName, ChangeAdd, nil, pcFields)

	// Add member entries
	for _, member := range op.Members {
		key := fmt.Sprintf("%s|%s", op.LAGName, member)
		cs.AddChange("PORTCHANNEL_MEMBER", key, ChangeAdd, nil, map[string]string{})
	}

	return cs, nil
}

// Execute applies the changes
func (op *CreateLAGOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Created LAG %s with members %v", op.LAGName, op.Members)
	return nil
}

// DeleteLAGOp deletes a Link Aggregation Group
type DeleteLAGOp struct {
	BaseOperation
	LAGName string
}

// Name returns the operation name
func (op *DeleteLAGOp) Name() string {
	return "lag.delete"
}

// Description returns a human-readable description
func (op *DeleteLAGOp) Description() string {
	return fmt.Sprintf("Delete LAG %s", op.LAGName)
}

// Validate checks all preconditions
func (op *DeleteLAGOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize LAG name (e.g., Po100 -> PortChannel100)
	op.LAGName = util.NormalizeInterfaceName(op.LAGName)

	checker := NewPreconditionChecker(d, op.Name(), op.LAGName)

	checker.RequireConnected()
	checker.RequireLocked()

	// LAG must exist
	checker.RequirePortChannelExists(op.LAGName)

	// LAG must not have a service bound
	if d.InterfaceHasService(op.LAGName) {
		checker.Check(false, "LAG must have no service bound",
			"remove the service from the LAG before deleting it")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *DeleteLAGOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	// Get current members
	pc, _ := d.GetPortChannel(op.LAGName)
	if pc != nil {
		for _, member := range pc.Members {
			key := fmt.Sprintf("%s|%s", op.LAGName, member)
			cs.AddChange("PORTCHANNEL_MEMBER", key, ChangeDelete, nil, nil)
		}
	}

	cs.AddChange("PORTCHANNEL", op.LAGName, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *DeleteLAGOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Deleted LAG %s", op.LAGName)
	return nil
}

// AddLAGMemberOp adds a member to an existing LAG
type AddLAGMemberOp struct {
	BaseOperation
	LAGName string
	Member  string
}

// Name returns the operation name
func (op *AddLAGMemberOp) Name() string {
	return "lag.add-member"
}

// Description returns a human-readable description
func (op *AddLAGMemberOp) Description() string {
	return fmt.Sprintf("Add member %s to LAG %s", op.Member, op.LAGName)
}

// Validate checks all preconditions
func (op *AddLAGMemberOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface names (e.g., Po100 -> PortChannel100, Eth0 -> Ethernet0)
	op.LAGName = util.NormalizeInterfaceName(op.LAGName)
	op.Member = util.NormalizeInterfaceName(op.Member)

	checker := NewPreconditionChecker(d, op.Name(), op.Member)

	checker.RequireConnected()
	checker.RequireLocked()

	// LAG must exist
	checker.RequirePortChannelExists(op.LAGName)

	// Member interface must exist
	checker.RequireInterfaceExists(op.Member)

	// Member must not already be in any LAG
	checker.RequireInterfaceNotLAGMember(op.Member)

	// Member must not have a service bound
	checker.RequireInterfaceNoService(op.Member)

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *AddLAGMemberOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())
	key := fmt.Sprintf("%s|%s", op.LAGName, op.Member)
	cs.AddChange("PORTCHANNEL_MEMBER", key, ChangeAdd, nil, map[string]string{})

	return cs, nil
}

// Execute applies the changes
func (op *AddLAGMemberOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Added member %s to LAG %s", op.Member, op.LAGName)
	return nil
}

// RemoveLAGMemberOp removes a member from a LAG
type RemoveLAGMemberOp struct {
	BaseOperation
	LAGName string
	Member  string
}

// Name returns the operation name
func (op *RemoveLAGMemberOp) Name() string {
	return "lag.remove-member"
}

// Description returns a human-readable description
func (op *RemoveLAGMemberOp) Description() string {
	return fmt.Sprintf("Remove member %s from LAG %s", op.Member, op.LAGName)
}

// Validate checks all preconditions
func (op *RemoveLAGMemberOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface names (e.g., Po100 -> PortChannel100, Eth0 -> Ethernet0)
	op.LAGName = util.NormalizeInterfaceName(op.LAGName)
	op.Member = util.NormalizeInterfaceName(op.Member)

	checker := NewPreconditionChecker(d, op.Name(), op.Member)

	checker.RequireConnected()
	checker.RequireLocked()

	// LAG must exist
	checker.RequirePortChannelExists(op.LAGName)

	// Member must be in this specific LAG
	checker.RequireInterfaceIsLAGMember(op.Member, op.LAGName)

	// Check we're not removing the last member if LAG has a service
	if d.InterfaceHasService(op.LAGName) {
		pc, err := d.GetPortChannel(op.LAGName)
		if err == nil && len(pc.Members) == 1 {
			checker.Check(false, "LAG must retain at least one member with active service",
				"remove the service from the LAG first, or add another member")
		}
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *RemoveLAGMemberOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())
	key := fmt.Sprintf("%s|%s", op.LAGName, op.Member)
	cs.AddChange("PORTCHANNEL_MEMBER", key, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *RemoveLAGMemberOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Removed member %s from LAG %s", op.Member, op.LAGName)
	return nil
}
