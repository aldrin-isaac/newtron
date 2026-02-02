package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// CreateACLTableOp creates a new ACL table
type CreateACLTableOp struct {
	BaseOperation
	TableName string
	Type      string // L3, L3V6, MIRROR
	Stage     string // ingress, egress
	Desc      string
	Ports     []string // Initial port bindings
}

// Name returns the operation name
func (op *CreateACLTableOp) Name() string {
	return "acl.create-table"
}

// Description returns a human-readable description
func (op *CreateACLTableOp) Description() string {
	return fmt.Sprintf("Create ACL table %s (%s, %s)", op.TableName, op.Type, op.Stage)
}

// Validate checks all preconditions
func (op *CreateACLTableOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface names (e.g., Eth0 -> Ethernet0)
	for i, port := range op.Ports {
		op.Ports[i] = util.NormalizeInterfaceName(port)
	}

	checker := NewPreconditionChecker(d, op.Name(), op.TableName)

	checker.RequireConnected()
	checker.RequireLocked()

	// ACL table must not already exist
	checker.RequireACLTableNotExists(op.TableName)

	// Validate type
	validTypes := map[string]bool{"L3": true, "L3V6": true, "MIRROR": true, "MIRRORV6": true}
	if !validTypes[op.Type] {
		checker.Check(false, "valid ACL type", "type must be L3, L3V6, MIRROR, or MIRRORV6")
	}

	// Validate stage
	if op.Stage != "ingress" && op.Stage != "egress" {
		checker.Check(false, "valid stage", "stage must be 'ingress' or 'egress'")
	}

	// All ports must exist
	for _, port := range op.Ports {
		checker.RequireInterfaceExists(port)
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *CreateACLTableOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	fields := map[string]string{
		"type":  op.Type,
		"stage": op.Stage,
	}
	if op.Desc != "" {
		fields["policy_desc"] = op.Desc
	}
	if len(op.Ports) > 0 {
		ports := ""
		for i, p := range op.Ports {
			if i > 0 {
				ports += ","
			}
			ports += p
		}
		fields["ports"] = ports
	}

	cs.AddChange("ACL_TABLE", op.TableName, ChangeAdd, nil, fields)

	return cs, nil
}

// Execute applies the changes
func (op *CreateACLTableOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Created ACL table %s", op.TableName)
	return nil
}

// DeleteACLTableOp deletes an ACL table
type DeleteACLTableOp struct {
	BaseOperation
	TableName string
}

// Name returns the operation name
func (op *DeleteACLTableOp) Name() string {
	return "acl.delete-table"
}

// Description returns a human-readable description
func (op *DeleteACLTableOp) Description() string {
	return fmt.Sprintf("Delete ACL table %s", op.TableName)
}

// Validate checks all preconditions
func (op *DeleteACLTableOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.TableName)

	checker.RequireConnected()
	checker.RequireLocked()

	// ACL table must exist
	checker.RequireACLTableExists(op.TableName)

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *DeleteACLTableOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	// Delete all rules first (would need to enumerate them)
	// Then delete the table
	cs.AddChange("ACL_TABLE", op.TableName, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *DeleteACLTableOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Deleted ACL table %s", op.TableName)
	return nil
}

// AddACLRuleOp adds a rule to an ACL table
type AddACLRuleOp struct {
	BaseOperation
	TableName string
	RuleName  string
	Priority  int

	// Match conditions
	SrcIP    string
	DstIP    string
	Protocol int
	SrcPort  int
	DstPort  string // Can be single port or range "1024-65535"
	DSCP     int

	// Action
	Action string // FORWARD, DROP
}

// Name returns the operation name
func (op *AddACLRuleOp) Name() string {
	return "acl.add-rule"
}

// Description returns a human-readable description
func (op *AddACLRuleOp) Description() string {
	return fmt.Sprintf("Add rule %s to ACL table %s", op.RuleName, op.TableName)
}

// Validate checks all preconditions
func (op *AddACLRuleOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.TableName)

	checker.RequireConnected()
	checker.RequireLocked()

	// ACL table must exist
	checker.RequireACLTableExists(op.TableName)

	// Validate priority
	if op.Priority < 1 || op.Priority > 65535 {
		checker.Check(false, "valid priority", "priority must be between 1 and 65535")
	}

	// Validate action
	if op.Action != "FORWARD" && op.Action != "DROP" {
		checker.Check(false, "valid action", "action must be FORWARD or DROP")
	}

	// Validate IPs if provided
	if op.SrcIP != "" && !util.IsValidIPv4CIDR(op.SrcIP) {
		checker.Check(false, "valid source IP", fmt.Sprintf("invalid CIDR: %s", op.SrcIP))
	}
	if op.DstIP != "" && !util.IsValidIPv4CIDR(op.DstIP) {
		checker.Check(false, "valid destination IP", fmt.Sprintf("invalid CIDR: %s", op.DstIP))
	}

	// Validate DSCP if provided
	if op.DSCP < 0 || op.DSCP > 63 {
		checker.Check(false, "valid DSCP", "DSCP must be between 0 and 63")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *AddACLRuleOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	key := fmt.Sprintf("%s|%s", op.TableName, op.RuleName)
	fields := map[string]string{
		"PRIORITY":      strconv.Itoa(op.Priority),
		"PACKET_ACTION": op.Action,
	}

	if op.SrcIP != "" {
		fields["SRC_IP"] = op.SrcIP
	}
	if op.DstIP != "" {
		fields["DST_IP"] = op.DstIP
	}
	if op.Protocol > 0 {
		fields["IP_PROTOCOL"] = strconv.Itoa(op.Protocol)
	}
	if op.SrcPort > 0 {
		fields["L4_SRC_PORT"] = strconv.Itoa(op.SrcPort)
	}
	if op.DstPort != "" {
		// Check if it's a range
		if _, _, err := util.ParsePortRange(op.DstPort); err == nil {
			if start, end, _ := util.ParsePortRange(op.DstPort); start != end {
				fields["L4_DST_PORT_RANGE"] = op.DstPort
			} else {
				fields["L4_DST_PORT"] = op.DstPort
			}
		}
	}
	if op.DSCP > 0 {
		fields["DSCP"] = strconv.Itoa(op.DSCP)
	}

	cs.AddChange("ACL_RULE", key, ChangeAdd, nil, fields)

	return cs, nil
}

// Execute applies the changes
func (op *AddACLRuleOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Added rule %s to ACL table %s", op.RuleName, op.TableName)
	return nil
}

// DeleteACLRuleOp removes a rule from an ACL table
type DeleteACLRuleOp struct {
	BaseOperation
	TableName string
	RuleName  string
}

// Name returns the operation name
func (op *DeleteACLRuleOp) Name() string {
	return "acl.delete-rule"
}

// Description returns a human-readable description
func (op *DeleteACLRuleOp) Description() string {
	return fmt.Sprintf("Delete rule %s from ACL table %s", op.RuleName, op.TableName)
}

// Validate checks all preconditions
func (op *DeleteACLRuleOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.TableName)

	checker.RequireConnected()
	checker.RequireLocked()

	// ACL table must exist
	checker.RequireACLTableExists(op.TableName)

	// Rule must exist (would need to check config_db)

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *DeleteACLRuleOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	key := fmt.Sprintf("%s|%s", op.TableName, op.RuleName)
	cs.AddChange("ACL_RULE", key, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *DeleteACLRuleOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Deleted rule %s from ACL table %s", op.RuleName, op.TableName)
	return nil
}
