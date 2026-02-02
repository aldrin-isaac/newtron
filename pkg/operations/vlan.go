package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// CreateVLANOp creates a new VLAN
type CreateVLANOp struct {
	BaseOperation
	ID       int
	VLANName string
	Desc     string
}

// Name returns the operation name
func (op *CreateVLANOp) Name() string {
	return "vlan.create"
}

// Description returns a human-readable description
func (op *CreateVLANOp) Description() string {
	return fmt.Sprintf("Create VLAN %d (%s)", op.ID, op.VLANName)
}

// Validate checks all preconditions
func (op *CreateVLANOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), fmt.Sprintf("Vlan%d", op.ID))

	checker.RequireConnected()
	checker.RequireLocked()

	// Validate VLAN ID
	if err := util.ValidateVLANID(op.ID); err != nil {
		checker.Check(false, "valid VLAN ID", err.Error())
	}

	// VLAN must not already exist
	checker.RequireVLANNotExists(op.ID)

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *CreateVLANOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.ID)
	fields := map[string]string{
		"vlanid":       strconv.Itoa(op.ID),
		"admin_status": "up",
	}
	if op.Desc != "" {
		fields["description"] = op.Desc
	}

	cs.AddChange("VLAN", vlanName, ChangeAdd, nil, fields)

	return cs, nil
}

// Execute applies the changes
func (op *CreateVLANOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Created VLAN %d (%s)", op.ID, op.VLANName)
	return nil
}

// DeleteVLANOp deletes a VLAN
type DeleteVLANOp struct {
	BaseOperation
	ID int
}

// Name returns the operation name
func (op *DeleteVLANOp) Name() string {
	return "vlan.delete"
}

// Description returns a human-readable description
func (op *DeleteVLANOp) Description() string {
	return fmt.Sprintf("Delete VLAN %d", op.ID)
}

// Validate checks all preconditions
func (op *DeleteVLANOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), fmt.Sprintf("Vlan%d", op.ID))

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist
	checker.RequireVLANExists(op.ID)

	// Check if VLAN has members
	vlan, err := d.GetVLAN(op.ID)
	if err == nil && len(vlan.Ports) > 0 {
		checker.Check(false, "VLAN must have no members",
			fmt.Sprintf("VLAN %d has %d port members - remove them first", op.ID, len(vlan.Ports)))
	}

	// Check if VLAN has an SVI with VRF binding
	if err == nil && vlan.SVIStatus != "" {
		checker.Check(false, "VLAN must have no SVI configured",
			"remove the SVI (VLAN interface) before deleting the VLAN")
	}

	// Check if VLAN has a MAC-VPN binding
	if err == nil && vlan.MACVPNInfo != nil && vlan.MACVPNInfo.L2VNI > 0 {
		checker.Check(false, "VLAN must have no EVPN mapping",
			"remove the MAC-VPN binding before deleting the VLAN")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *DeleteVLANOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())
	vlanName := fmt.Sprintf("Vlan%d", op.ID)
	cs.AddChange("VLAN", vlanName, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *DeleteVLANOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Deleted VLAN %d", op.ID)
	return nil
}

// AddVLANMemberOp adds a port to a VLAN
type AddVLANMemberOp struct {
	BaseOperation
	VLANID int
	Port   string
	Tagged bool
}

// Name returns the operation name
func (op *AddVLANMemberOp) Name() string {
	return "vlan.add-member"
}

// Description returns a human-readable description
func (op *AddVLANMemberOp) Description() string {
	mode := "untagged"
	if op.Tagged {
		mode = "tagged"
	}
	return fmt.Sprintf("Add %s port %s to VLAN %d", mode, op.Port, op.VLANID)
}

// Validate checks all preconditions
func (op *AddVLANMemberOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Port = util.NormalizeInterfaceName(op.Port)

	checker := NewPreconditionChecker(d, op.Name(), op.Port)

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist
	checker.RequireVLANExists(op.VLANID)

	// Port must exist
	checker.RequireInterfaceExists(op.Port)

	// Port must not have a routed (L3) service - mutually exclusive with L2
	// This would check if the interface has an IP address or VRF binding
	intf, err := d.GetInterface(op.Port)
	if err == nil && len(intf.IPAddresses()) > 0 {
		checker.Check(false, "port must not have IP addresses",
			"L2 (VLAN) and L3 (routed) configuration are mutually exclusive - remove IP first")
	}
	if err == nil && intf.VRF() != "" {
		checker.Check(false, "port must not be in a VRF",
			"L2 (VLAN) and L3 (routed) configuration are mutually exclusive - remove VRF binding first")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *AddVLANMemberOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.VLANID)
	key := fmt.Sprintf("%s|%s", vlanName, op.Port)

	mode := "untagged"
	if op.Tagged {
		mode = "tagged"
	}

	cs.AddChange("VLAN_MEMBER", key, ChangeAdd, nil, map[string]string{
		"tagging_mode": mode,
	})

	return cs, nil
}

// Execute applies the changes
func (op *AddVLANMemberOp) Execute(ctx context.Context, d *network.Device) error {
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

	mode := "untagged"
	if op.Tagged {
		mode = "tagged"
	}
	util.WithDevice(d.Name()).Infof("Added %s port %s to VLAN %d", mode, op.Port, op.VLANID)
	return nil
}

// RemoveVLANMemberOp removes a port from a VLAN
type RemoveVLANMemberOp struct {
	BaseOperation
	VLANID int
	Port   string
}

// Name returns the operation name
func (op *RemoveVLANMemberOp) Name() string {
	return "vlan.remove-member"
}

// Description returns a human-readable description
func (op *RemoveVLANMemberOp) Description() string {
	return fmt.Sprintf("Remove port %s from VLAN %d", op.Port, op.VLANID)
}

// Validate checks all preconditions
func (op *RemoveVLANMemberOp) Validate(ctx context.Context, d *network.Device) error {
	// Normalize interface name (e.g., Eth0 -> Ethernet0)
	op.Port = util.NormalizeInterfaceName(op.Port)

	checker := NewPreconditionChecker(d, op.Name(), op.Port)

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist
	checker.RequireVLANExists(op.VLANID)

	// Port must be a member of this VLAN
	vlan, err := d.GetVLAN(op.VLANID)
	if err == nil {
		found := false
		for _, p := range vlan.Ports {
			// Ports may be stored as "Ethernet0" or "Ethernet0(t)"
			if p == op.Port || p == op.Port+"(t)" {
				found = true
				break
			}
		}
		if !found {
			checker.Check(false, "port must be a VLAN member",
				fmt.Sprintf("port %s is not a member of VLAN %d", op.Port, op.VLANID))
		}
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *RemoveVLANMemberOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.VLANID)
	key := fmt.Sprintf("%s|%s", vlanName, op.Port)
	cs.AddChange("VLAN_MEMBER", key, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *RemoveVLANMemberOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Removed port %s from VLAN %d", op.Port, op.VLANID)
	return nil
}

// ConfigureSVIOp configures a VLAN's SVI (Layer 3 interface)
type ConfigureSVIOp struct {
	BaseOperation
	VLANID         int
	VRF            string
	IPAddress      string
	AnycastGateway string
	AnycastMAC     string
}

// Name returns the operation name
func (op *ConfigureSVIOp) Name() string {
	return "vlan.configure-svi"
}

// Description returns a human-readable description
func (op *ConfigureSVIOp) Description() string {
	return fmt.Sprintf("Configure SVI for VLAN %d", op.VLANID)
}

// Validate checks all preconditions
func (op *ConfigureSVIOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), fmt.Sprintf("Vlan%d", op.VLANID))

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist first
	checker.RequireVLANExists(op.VLANID)

	// VRF must exist if specified
	if op.VRF != "" && op.VRF != "default" {
		checker.RequireVRFExists(op.VRF)
	}

	// Validate IP address if provided
	if op.IPAddress != "" && !util.IsValidIPv4CIDR(op.IPAddress) {
		checker.Check(false, "valid IP address", fmt.Sprintf("invalid CIDR: %s", op.IPAddress))
	}

	// Anycast gateway requires IP address
	if op.AnycastGateway != "" && op.IPAddress == "" {
		checker.Check(false, "IP address required for anycast gateway",
			"specify an IP address along with the anycast gateway")
	}

	// Validate anycast MAC if provided
	if op.AnycastMAC != "" && !util.IsValidMACAddress(op.AnycastMAC) {
		checker.Check(false, "valid anycast MAC", fmt.Sprintf("invalid MAC: %s", op.AnycastMAC))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *ConfigureSVIOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.VLANID)

	// VRF binding
	if op.VRF != "" {
		cs.AddChange("VLAN_INTERFACE", vlanName, ChangeAdd, nil, map[string]string{
			"vrf_name": op.VRF,
		})
	} else {
		cs.AddChange("VLAN_INTERFACE", vlanName, ChangeAdd, nil, map[string]string{})
	}

	// IP address
	if op.IPAddress != "" {
		key := fmt.Sprintf("%s|%s", vlanName, op.IPAddress)
		cs.AddChange("VLAN_INTERFACE", key, ChangeAdd, nil, map[string]string{})
	}

	// Anycast gateway
	if op.AnycastMAC != "" {
		cs.AddChange("SAG_GLOBAL", "IPv4", ChangeAdd, nil, map[string]string{
			"gwmac": op.AnycastMAC,
		})
	}

	return cs, nil
}

// Execute applies the changes
func (op *ConfigureSVIOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Configured SVI for VLAN %d", op.VLANID)
	return nil
}
