package operations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/util"
)

// CreateVTEPOp creates the VXLAN Tunnel Endpoint
type CreateVTEPOp struct {
	BaseOperation
	VTEPName string
	SourceIP string
}

// Name returns the operation name
func (op *CreateVTEPOp) Name() string {
	return "evpn.create-vtep"
}

// Description returns a human-readable description
func (op *CreateVTEPOp) Description() string {
	return fmt.Sprintf("Create VTEP %s with source IP %s", op.VTEPName, op.SourceIP)
}

// Validate checks all preconditions
func (op *CreateVTEPOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.VTEPName)

	checker.RequireConnected()
	checker.RequireLocked()

	// Validate source IP
	if !util.IsValidIPv4(op.SourceIP) {
		checker.Check(false, "valid source IP", fmt.Sprintf("invalid IP: %s", op.SourceIP))
	}

	// Check if VTEP already exists
	if d.VTEPExists() {
		checker.Check(false, "VTEP must not exist", "VTEP already configured - only one VTEP per device")
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *CreateVTEPOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	cs.AddChange("VXLAN_TUNNEL", op.VTEPName, ChangeAdd, nil, map[string]string{
		"src_ip": op.SourceIP,
	})

	// Create NVO entry
	cs.AddChange("VXLAN_EVPN_NVO", "nvo1", ChangeAdd, nil, map[string]string{
		"source_vtep": op.VTEPName,
	})

	return cs, nil
}

// Execute applies the changes
func (op *CreateVTEPOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Created VTEP %s with source IP %s", op.VTEPName, op.SourceIP)
	return nil
}

// CreateVRFOp creates a VRF with L3VNI for EVPN
type CreateVRFOp struct {
	BaseOperation
	VRFName  string
	L3VNI    int
	ImportRT []string
	ExportRT []string
}

// Name returns the operation name
func (op *CreateVRFOp) Name() string {
	return "evpn.create-vrf"
}

// Description returns a human-readable description
func (op *CreateVRFOp) Description() string {
	return fmt.Sprintf("Create VRF %s with L3VNI %d", op.VRFName, op.L3VNI)
}

// Validate checks all preconditions
func (op *CreateVRFOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.VRFName)

	checker.RequireConnected()
	checker.RequireLocked()

	// VRF must not already exist
	checker.RequireVRFNotExists(op.VRFName)

	// Validate VNI if provided
	if op.L3VNI > 0 {
		if err := util.ValidateVNI(op.L3VNI); err != nil {
			checker.Check(false, "valid L3VNI", err.Error())
		}

		// VTEP must exist for EVPN
		checker.RequireVTEPConfigured()
		checker.RequireBGPConfigured()
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *CreateVRFOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vrfFields := map[string]string{}
	if op.L3VNI > 0 {
		vrfFields["vni"] = strconv.Itoa(op.L3VNI)
	}

	cs.AddChange("VRF", op.VRFName, ChangeAdd, nil, vrfFields)

	// Add L3VNI mapping to VTEP if VNI specified
	if op.L3VNI > 0 {
		// Get VTEP name
		vtepName := "vtep1" // Would need to look this up
		mapKey := fmt.Sprintf("%s|map_%d_%s", vtepName, op.L3VNI, op.VRFName)
		cs.AddChange("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
			"vrf": op.VRFName,
			"vni": strconv.Itoa(op.L3VNI),
		})
	}

	return cs, nil
}

// Execute applies the changes
func (op *CreateVRFOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Created VRF %s with L3VNI %d", op.VRFName, op.L3VNI)
	return nil
}

// DeleteVRFOp deletes a VRF
type DeleteVRFOp struct {
	BaseOperation
	VRFName string
}

// Name returns the operation name
func (op *DeleteVRFOp) Name() string {
	return "evpn.delete-vrf"
}

// Description returns a human-readable description
func (op *DeleteVRFOp) Description() string {
	return fmt.Sprintf("Delete VRF %s", op.VRFName)
}

// Validate checks all preconditions
func (op *DeleteVRFOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), op.VRFName)

	checker.RequireConnected()
	checker.RequireLocked()

	// VRF must exist
	checker.RequireVRFExists(op.VRFName)

	// VRF must have no interfaces
	vrf, err := d.GetVRF(op.VRFName)
	if err == nil && len(vrf.Interfaces) > 0 {
		checker.Check(false, "VRF must have no interfaces",
			fmt.Sprintf("VRF %s has %d interfaces - unbind them first", op.VRFName, len(vrf.Interfaces)))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *DeleteVRFOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	// Remove VNI mapping first
	vrf, _ := d.GetVRF(op.VRFName)
	if vrf != nil && vrf.L3VNI > 0 {
		// Find and delete the VXLAN_TUNNEL_MAP entry
		vtepName := "vtep1"
		mapKey := fmt.Sprintf("%s|map_%d_%s", vtepName, vrf.L3VNI, op.VRFName)
		cs.AddChange("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
	}

	cs.AddChange("VRF", op.VRFName, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *DeleteVRFOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Deleted VRF %s", op.VRFName)
	return nil
}

// MapL2VNIOp creates an L2VNI mapping for a VLAN
type MapL2VNIOp struct {
	BaseOperation
	VLANID         int
	VNI            int
	ARPSuppression bool
}

// Name returns the operation name
func (op *MapL2VNIOp) Name() string {
	return "evpn.map-l2vni"
}

// Description returns a human-readable description
func (op *MapL2VNIOp) Description() string {
	return fmt.Sprintf("Map VLAN %d to L2VNI %d", op.VLANID, op.VNI)
}

// Validate checks all preconditions
func (op *MapL2VNIOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), fmt.Sprintf("Vlan%d", op.VLANID))

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist first
	checker.RequireVLANExists(op.VLANID)

	// VTEP must be configured
	checker.RequireVTEPConfigured()

	// BGP must be configured for EVPN
	checker.RequireBGPConfigured()

	// Validate VNI
	if err := util.ValidateVNI(op.VNI); err != nil {
		checker.Check(false, "valid VNI", err.Error())
	}

	// Check if VLAN already has a VNI mapping
	vlan, err := d.GetVLAN(op.VLANID)
	if err == nil && vlan.MACVPNInfo != nil && vlan.MACVPNInfo.L2VNI > 0 {
		checker.Check(false, "VLAN must not have existing VNI mapping",
			fmt.Sprintf("VLAN %d already mapped to VNI %d", op.VLANID, vlan.MACVPNInfo.L2VNI))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *MapL2VNIOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.VLANID)
	vtepName := "vtep1"
	mapKey := fmt.Sprintf("%s|map_%d_%s", vtepName, op.VNI, vlanName)

	cs.AddChange("VXLAN_TUNNEL_MAP", mapKey, ChangeAdd, nil, map[string]string{
		"vlan": vlanName,
		"vni":  strconv.Itoa(op.VNI),
	})

	// ARP suppression
	if op.ARPSuppression {
		cs.AddChange("SUPPRESS_VLAN_NEIGH", vlanName, ChangeAdd, nil, map[string]string{
			"suppress": "on",
		})
	}

	return cs, nil
}

// Execute applies the changes
func (op *MapL2VNIOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Mapped VLAN %d to L2VNI %d", op.VLANID, op.VNI)
	return nil
}

// UnmapL2VNIOp removes an L2VNI mapping
type UnmapL2VNIOp struct {
	BaseOperation
	VLANID int
}

// Name returns the operation name
func (op *UnmapL2VNIOp) Name() string {
	return "evpn.unmap-l2vni"
}

// Description returns a human-readable description
func (op *UnmapL2VNIOp) Description() string {
	return fmt.Sprintf("Unmap L2VNI from VLAN %d", op.VLANID)
}

// Validate checks all preconditions
func (op *UnmapL2VNIOp) Validate(ctx context.Context, d *network.Device) error {
	checker := NewPreconditionChecker(d, op.Name(), fmt.Sprintf("Vlan%d", op.VLANID))

	checker.RequireConnected()
	checker.RequireLocked()

	// VLAN must exist
	checker.RequireVLANExists(op.VLANID)

	// VLAN must have a VNI mapping
	vlan, err := d.GetVLAN(op.VLANID)
	if err == nil && (vlan.MACVPNInfo == nil || vlan.MACVPNInfo.L2VNI == 0) {
		checker.Check(false, "VLAN must have VNI mapping",
			fmt.Sprintf("VLAN %d has no VNI mapping", op.VLANID))
	}

	if err := checker.Result(); err != nil {
		return err
	}

	op.MarkValidated()
	return nil
}

// Preview returns the changes
func (op *UnmapL2VNIOp) Preview(ctx context.Context, d *network.Device) (*ChangeSet, error) {
	if err := op.RequireValidated(); err != nil {
		if err := op.Validate(ctx, d); err != nil {
			return nil, err
		}
	}

	cs := NewChangeSet(d.Name(), op.Name())

	vlanName := fmt.Sprintf("Vlan%d", op.VLANID)
	vlan, _ := d.GetVLAN(op.VLANID)

	if vlan != nil && vlan.MACVPNInfo != nil && vlan.MACVPNInfo.L2VNI > 0 {
		vtepName := "vtep1"
		mapKey := fmt.Sprintf("%s|map_%d_%s", vtepName, vlan.MACVPNInfo.L2VNI, vlanName)
		cs.AddChange("VXLAN_TUNNEL_MAP", mapKey, ChangeDelete, nil, nil)
	}

	// Remove ARP suppression
	cs.AddChange("SUPPRESS_VLAN_NEIGH", vlanName, ChangeDelete, nil, nil)

	return cs, nil
}

// Execute applies the changes
func (op *UnmapL2VNIOp) Execute(ctx context.Context, d *network.Device) error {
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

	util.WithDevice(d.Name()).Infof("Unmapped L2VNI from VLAN %d", op.VLANID)
	return nil
}
